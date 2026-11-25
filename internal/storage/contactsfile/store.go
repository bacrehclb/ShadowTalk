// Package contactsfile stores public contact pins in a profile-owned JSON file.
package contactsfile

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/gofrs/flock"
)

const (
	fileName    = "contacts.json"
	maxFileSize = 1 << 20
)

var (
	ErrInvalidRecord      = errors.New("invalid saved contacts")
	ErrOwnerMismatch      = errors.New("contacts belong to a different profile identity")
	ErrUnsupportedVersion = errors.New("unsupported contacts version")
	ErrBusy               = errors.New("contacts are in use by another operation")
)

type record struct {
	Version  int     `json:"version"`
	Owner    string  `json:"owner"`
	Contacts []entry `json:"contacts"`
}

type entry struct {
	Alias     string    `json:"alias"`
	Nickname  string    `json:"nickname"`
	Onion     string    `json:"onion"`
	PublicKey string    `json:"public_key"`
	AddedAt   time.Time `json:"added_at"`
}

type Store struct {
	directory string
	owner     [32]byte
}

// New does no I/O. Owner is the public key from the loaded profile metadata.
func New(directory string, owner [32]byte) *Store { return &Store{directory, owner} }

func (s *Store) Read() (*contacts.Book, error) {
	if s.owner == [32]byte{} {
		return nil, ErrOwnerMismatch
	}
	path := filepath.Join(s.directory, fileName)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return contacts.RestoreBook(s.owner, nil)
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileSize {
		return nil, ErrInvalidRecord
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrInvalidRecord
	}
	data, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileSize {
		return nil, ErrInvalidRecord
	}
	return s.decode(data)
}

func (s *Store) decode(data []byte) (*contacts.Book, error) {
	var saved record
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, ErrInvalidRecord
	}
	if saved.Version != 1 {
		if saved.Version > 1 {
			return nil, ErrUnsupportedVersion
		}
		return nil, ErrInvalidRecord
	}
	if saved.Owner != hex.EncodeToString(s.owner[:]) {
		return nil, ErrOwnerMismatch
	}
	// Ignore indentation, but reject ambiguous duplicate fields, extra fields,
	// null lists and alternative encodings. This file is application-managed.
	var compact bytes.Buffer
	canonical, err := json.Marshal(saved)
	if err != nil || json.Compact(&compact, data) != nil || !bytes.Equal(canonical, compact.Bytes()) || saved.Contacts == nil {
		return nil, ErrInvalidRecord
	}
	if len(saved.Contacts) > contacts.MaxContacts {
		return nil, ErrInvalidRecord
	}
	entries := make([]contacts.Contact, 0, len(saved.Contacts))
	for _, item := range saved.Contacts {
		public, err := hex.DecodeString(item.PublicKey)
		if err != nil || len(public) != 32 || hex.EncodeToString(public) != item.PublicKey {
			return nil, ErrInvalidRecord
		}
		entries = append(entries, contacts.Contact{Alias: item.Alias, Nickname: item.Nickname, Onion: item.Onion, PublicKey: [32]byte(public), AddedAt: item.AddedAt})
	}
	book, err := contacts.RestoreBook(s.owner, entries)
	if err != nil {
		return nil, ErrInvalidRecord
	}
	return book, nil
}

// Update serializes writers across processes. Corrupt files and callback errors
// are preserved without replacement. A failed write never falls back to reset.
func (s *Store) Update(change func(*contacts.Book) error) (err error) {
	if s.owner == [32]byte{} {
		return ErrOwnerMismatch
	}
	if err := os.MkdirAll(s.directory, 0700); err != nil {
		return err
	}
	lockPath := filepath.Join(s.directory, ".contacts.lock")
	info, err := os.Lstat(lockPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && !info.Mode().IsRegular() {
		return ErrInvalidRecord
	}
	lock := flock.New(lockPath, flock.SetPermissions(0600))
	defer func() { err = errors.Join(err, lock.Close()) }()
	locked, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !locked {
		return ErrBusy
	}
	book, err := s.Read()
	if err != nil {
		return err
	}
	before, err := s.encode(book)
	if err != nil {
		return err
	}
	if err := change(book); err != nil {
		return err
	}
	after, err := s.encode(book)
	if err != nil {
		return err
	}
	if bytes.Equal(before, after) {
		return nil
	}
	return s.publish(after)
}

func (s *Store) encode(book *contacts.Book) ([]byte, error) {
	if book == nil || book.Owner() != s.owner {
		return nil, ErrOwnerMismatch
	}
	saved := record{Version: 1, Owner: hex.EncodeToString(s.owner[:]), Contacts: make([]entry, 0)}
	for _, contact := range book.List() {
		saved.Contacts = append(saved.Contacts, entry{
			Alias: contact.Alias, Nickname: contact.Nickname, Onion: contact.Onion,
			PublicKey: hex.EncodeToString(contact.PublicKey[:]), AddedAt: contact.AddedAt.UTC(),
		})
	}
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return nil, ErrInvalidRecord
	}
	data = append(data, '\n')
	if len(data) > maxFileSize {
		return nil, ErrInvalidRecord
	}
	return data, nil
}

func (s *Store) publish(data []byte) error {
	file, err := os.CreateTemp(s.directory, ".contacts-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(s.directory, fileName))
}
