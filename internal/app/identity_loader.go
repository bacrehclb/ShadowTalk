package app

import (
	"fmt"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

// EncryptedSeedReader reads a saved ciphertext and its encryption key ID.
// Errors must not include secret values.
type EncryptedSeedReader interface {
	Load() (keyID string, encryptedSeed []byte, err error)
}

// EncryptionKeyReader reads the key addressed by ID. Returned bytes belong to
// the caller and may be cleared, including on error. Errors must not include
// secret values.
type EncryptionKeyReader interface {
	Get(id string) ([]byte, error)
}

type IdentityLoader struct {
	seeds EncryptedSeedReader
	keys  EncryptionKeyReader
}

// NewIdentityLoader connects readers without accessing either store.
func NewIdentityLoader(seeds EncryptedSeedReader, keys EncryptionKeyReader) *IdentityLoader {
	return &IdentityLoader{seeds: seeds, keys: keys}
}

// Load restores an existing identity without creating or replacing any data.
// Every failure returns an empty KeyPair; no new identity is generated.
func (l *IdentityLoader) Load() (identity.KeyPair, error) {
	keyID, encryptedSeed, err := l.seeds.Load()
	if err != nil {
		return identity.KeyPair{}, fmt.Errorf("load identity seed: %w", err)
	}
	return restoreIdentity(keyID, encryptedSeed, l.keys)
}

func restoreIdentity(keyID string, encryptedSeed []byte, keys EncryptionKeyReader) (identity.KeyPair, error) {
	if err := identity.ValidateEncryptionKeyID(keyID); err != nil {
		return identity.KeyPair{}, fmt.Errorf("load identity seed: %w", err)
	}
	key, err := keys.Get(keyID)
	defer clear(key)
	if err != nil {
		return identity.KeyPair{}, fmt.Errorf("load identity encryption key: %w", err)
	}
	seed, err := identity.DecryptSeed(encryptedSeed, key)
	if err != nil {
		return identity.KeyPair{}, fmt.Errorf("decrypt identity seed: %w", err)
	}
	defer clear(seed)
	// RestoreKeyPair copies the seed, so clearing it does not erase the result.
	return identity.RestoreKeyPair(seed)
}
