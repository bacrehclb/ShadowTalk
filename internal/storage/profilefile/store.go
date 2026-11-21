// Package profilefile stores local profile metadata in a versioned JSON file.
// It does not store private keys or message history.
package profilefile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

const (
	fileName       = "profile.json"
	formatVersion  = 1
	maxProfileSize = 4096
)

var ErrUnsupportedVersion = errors.New("unsupported profile version")

// record is a disk format, not a domain or network model.
type record struct {
	Version   int       `json:"version"`
	Nickname  string    `json:"nickname"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	directory string
}

// New creates a store without accessing the filesystem.
func New(directory string) *Store {
	return &Store{directory: directory}
}

func (s *Store) Create(profile identity.Profile) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record{
		Version:   formatVersion,
		Nickname:  profile.Nickname,
		CreatedAt: profile.CreatedAt.UTC(),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode profile: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(s.directory, 0o700); err != nil {
		return fmt.Errorf("create profile directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.directory, ".profile-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary profile: %w", err)
	}
	defer os.Remove(temporary.Name())

	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write profile: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync profile: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close profile: %w", err)
	}

	// Link publishes the complete file without replacing an existing entry.
	// Unlike a check followed by Rename, this also protects concurrent creators.
	// Both paths are in the same directory, so no cross-filesystem link is needed.
	if err := os.Link(temporary.Name(), filepath.Join(s.directory, fileName)); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return identity.ErrProfileExists
		}
		return fmt.Errorf("publish profile: %w", err)
	}
	return nil
}

func (s *Store) Load() (identity.Profile, error) {
	file, err := os.Open(filepath.Join(s.directory, fileName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return identity.Profile{}, identity.ErrProfileNotFound
		}
		return identity.Profile{}, fmt.Errorf("open profile: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return identity.Profile{}, fmt.Errorf("stat profile: %w", err)
	}
	if !info.Mode().IsRegular() {
		return identity.Profile{}, fmt.Errorf("%w: expected a regular file", identity.ErrInvalidProfile)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxProfileSize+1))
	if err != nil {
		return identity.Profile{}, fmt.Errorf("read profile: %w", err)
	}
	if len(data) > maxProfileSize {
		return identity.Profile{}, fmt.Errorf("%w: file is too large", identity.ErrInvalidProfile)
	}

	var saved *record
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&saved); err != nil {
		return identity.Profile{}, fmt.Errorf("%w: decode record: %v", identity.ErrInvalidProfile, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return identity.Profile{}, fmt.Errorf("%w: trailing data", identity.ErrInvalidProfile)
	}
	if saved == nil || saved.Version < 1 {
		return identity.Profile{}, fmt.Errorf("%w: missing format version", identity.ErrInvalidProfile)
	}
	if saved.Version != formatVersion {
		return identity.Profile{}, fmt.Errorf("%w: %d", ErrUnsupportedVersion, saved.Version)
	}
	profile := identity.Profile{
		Nickname:  saved.Nickname,
		CreatedAt: saved.CreatedAt.UTC(),
	}
	if err := profile.Validate(); err != nil {
		return identity.Profile{}, fmt.Errorf("%w: %v", identity.ErrInvalidProfile, err)
	}
	return profile, nil
}
