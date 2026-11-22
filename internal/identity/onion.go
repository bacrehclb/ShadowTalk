package identity

import (
	"crypto/hkdf"
	"crypto/sha256"
)

// OnionServiceSeed derives a separate, stable Ed25519 seed for the Tor service.
// The returned bytes are secret and owned by the caller. The purpose string is
// part of the persistent identity format: changing it changes every onion address.
func (k KeyPair) OnionServiceSeed() ([]byte, error) {
	if _, err := k.Signer(); err != nil {
		return nil, err
	}
	seed, err := k.Seed()
	if err != nil {
		return nil, err
	}
	defer clear(seed)
	return hkdf.Key(sha256.New, seed, nil, "ShadowTalk/onion-service/v1", 32)
}
