package identity

import (
	"crypto/hkdf"
	"crypto/sha256"
)

// HistoryKey derives a separate stable root key for local message encryption.
// The returned secret belongs to the caller and must not be logged or stored
// in plaintext. The purpose string is part of the persistent storage format.
func (k KeyPair) HistoryKey() ([]byte, error) {
	if _, err := k.Signer(); err != nil {
		return nil, err
	}
	seed, err := k.Seed()
	if err != nil {
		return nil, err
	}
	defer clear(seed)
	return hkdf.Key(sha256.New, seed, nil, "ShadowTalk/message-history/v1", 32)
}
