package identity

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha256"

	"github.com/bacrehclb/ShadowTalk/internal/onion"
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

// OnionAddress derives the public service address offline. No Tor process or
// network access is needed to create an invitation for this identity.
func (k KeyPair) OnionAddress() (string, error) {
	seed, err := k.OnionServiceSeed()
	if err != nil {
		return "", err
	}
	defer clear(seed)
	private := ed25519.NewKeyFromSeed(seed)
	defer clear(private)
	return onion.FromPublicKey([32]byte(private[32:])), nil
}
