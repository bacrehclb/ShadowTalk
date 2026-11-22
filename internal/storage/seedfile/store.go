// Package seedfile stores an already encrypted identity seed and a reference
// to its encryption key. It never reads the system key store or decrypts data.
package seedfile

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

const (
	fileName          = "identity-seed.json"
	formatVersion     = 1
	maxFileSize       = 4096
	encryptedSeedSize = 12 + 32 + 16 // v1: GCM nonce, encrypted seed, authentication tag.
)

var (
	ErrNotFound           = errors.New("encrypted seed file not found")
	ErrExists             = errors.New("encrypted seed file already exists")
	ErrInvalidRecord      = errors.New("invalid encrypted seed record")
	ErrUnsupportedVersion = errors.New("unsupported encrypted seed version")
)

// record is the versioned disk format, not a domain or network model.
type record struct {
	Version       int    `json:"version"`
	KeyID         string `json:"key_id"`
	EncryptedSeed string `json:"encrypted_seed"`
}

type Store struct {
	directory string
}

// New creates a store without accessing the filesystem.
func New(directory string) *Store {
	return &Store{directory: directory}
}

// Create publishes a complete record without replacing any existing entry.
// encryptedSeed must come from identity.EncryptSeed; length validation alone
// cannot establish that the caller supplied authentic ciphertext.
func (s *Store) Create(keyID string, encryptedSeed []byte) error {
	if err := identity.ValidateEncryptionKeyID(keyID); err != nil {
		return err
	}
	if len(encryptedSeed) != encryptedSeedSize {
		return fmt.Errorf("%w: incorrect ciphertext length", ErrInvalidRecord)
	}
	data, err := json.MarshalIndent(record{
		Version:       formatVersion,
		KeyID:         keyID,
		EncryptedSeed: base64.StdEncoding.EncodeToString(encryptedSeed),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode encrypted seed: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(s.directory, 0o700); err != nil {
		return fmt.Errorf("create seed directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.directory, ".identity-seed-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary seed file: %w", err)
	}
	defer os.Remove(temporary.Name())

	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write encrypted seed: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync encrypted seed: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close encrypted seed: %w", err)
	}
	// As in profilefile, Link publishes without overwriting even when creators
	// race. Filesystems without hard links return an error; there is no fallback.
	if err := os.Link(temporary.Name(), filepath.Join(s.directory, fileName)); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrExists
		}
		return fmt.Errorf("publish encrypted seed: %w", err)
	}
	return nil
}

// Load returns the key ID and a fresh ciphertext slice, or empty values on
// failure. It checks the format only; the caller must use identity.DecryptSeed
// to authenticate and decrypt the ciphertext. Load never repairs a record.
func (s *Store) Load() (keyID string, encryptedSeed []byte, err error) {
	path := filepath.Join(s.directory, fileName)
	// Reject directories and special files before opening (a FIFO could block).
	// Like other ordinary file reads, Stat/Open follow symbolic links.
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil, ErrNotFound
		}
		return "", nil, fmt.Errorf("stat encrypted seed: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%w: expected a regular file", ErrInvalidRecord)
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil, ErrNotFound
		}
		return "", nil, fmt.Errorf("open encrypted seed: %w", err)
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return "", nil, fmt.Errorf("stat opened seed file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%w: expected a regular file", ErrInvalidRecord)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return "", nil, fmt.Errorf("read encrypted seed: %w", err)
	}
	if len(data) > maxFileSize {
		return "", nil, fmt.Errorf("%w: file is too large", ErrInvalidRecord)
	}

	var saved *record
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&saved); err != nil {
		// Do not include input data or JSON decoder diagnostics in errors.
		return "", nil, fmt.Errorf("%w: cannot decode record", ErrInvalidRecord)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return "", nil, fmt.Errorf("%w: trailing data", ErrInvalidRecord)
	}
	if saved == nil || saved.Version < 1 {
		return "", nil, fmt.Errorf("%w: missing or invalid version", ErrInvalidRecord)
	}
	if saved.Version != formatVersion {
		return "", nil, ErrUnsupportedVersion
	}
	if err := identity.ValidateEncryptionKeyID(saved.KeyID); err != nil {
		return "", nil, fmt.Errorf("%w: invalid key ID", ErrInvalidRecord)
	}
	if len(saved.EncryptedSeed) != base64.StdEncoding.EncodedLen(encryptedSeedSize) {
		return "", nil, fmt.Errorf("%w: incorrect ciphertext length", ErrInvalidRecord)
	}
	encryptedSeed, err = base64.StdEncoding.Strict().DecodeString(saved.EncryptedSeed)
	if err != nil || len(encryptedSeed) != encryptedSeedSize {
		return "", nil, fmt.Errorf("%w: invalid ciphertext encoding", ErrInvalidRecord)
	}
	return saved.KeyID, encryptedSeed, nil
}
