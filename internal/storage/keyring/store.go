// Package keyring stores seed encryption keys in the operating system's secret
// store. It never falls back to a plaintext file.
package keyring

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
	oskeyring "github.com/zalando/go-keyring"
)

const service = "ShadowTalk/seed-encryption/v1"

var (
	ErrInvalidKeyID     = identity.ErrInvalidEncryptionKeyID
	ErrNotFound         = errors.New("encryption key not found in system key store")
	ErrUnavailable      = errors.New("system key store access failed")
	ErrInvalidStoredKey = errors.New("invalid encryption key in system key store")
	ErrWriteUnconfirmed = errors.New("system key store write could not be verified")
)

// Store addresses keys by a stable, non-secret ID supplied by the caller.
// IDs must not be nicknames: nicknames can change and are not unique.
// Construct a Store with New; its zero value is not usable.
type Store struct {
	set func(service, account, secret string) error
	get func(service, account string) (string, error)
}

// New creates an adapter without accessing the system key store.
func New() *Store {
	return &Store{set: oskeyring.Set, get: oskeyring.Get}
}

// Set writes a separate, cryptographically random seed encryption key and
// verifies it by reading it back. An existing key under the same ID is replaced.
// Callers must coordinate writes and must not replace a key still needed to
// decrypt a saved seed. An error does not guarantee that no write took place.
func (s *Store) Set(id string, key []byte) error {
	if err := identity.ValidateEncryptionKeyID(id); err != nil {
		return err
	}
	if len(key) != identity.SeedEncryptionKeySize {
		return identity.ErrInvalidEncryptionKeySize
	}
	if err := s.set(service, id, base64.StdEncoding.EncodeToString(key)); err != nil {
		// Backend errors may contain secret values; do not expose their text.
		return fmt.Errorf("set encryption key: %w", ErrUnavailable)
	}
	stored, err := s.Get(id)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrWriteUnconfirmed, err)
	}
	if !bytes.Equal(stored, key) {
		return ErrWriteUnconfirmed
	}
	return nil
}

// Get returns a fresh 32-byte slice, or nil on error. Missing, inaccessible and
// malformed entries are distinct errors; Get never creates or repairs a key.
func (s *Store) Get(id string) ([]byte, error) {
	if err := identity.ValidateEncryptionKeyID(id); err != nil {
		return nil, err
	}
	encoded, err := s.get(service, id)
	if err != nil {
		if errors.Is(err, oskeyring.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get encryption key: %w", ErrUnavailable)
	}
	if len(encoded) != base64.StdEncoding.EncodedLen(identity.SeedEncryptionKeySize) {
		return nil, ErrInvalidStoredKey
	}
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(key) != identity.SeedEncryptionKeySize {
		return nil, ErrInvalidStoredKey
	}
	return key, nil
}
