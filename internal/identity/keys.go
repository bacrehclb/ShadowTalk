package identity

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
)

var (
	ErrInvalidSeedSize = errors.New("invalid identity seed size")
	ErrInvalidKeyPair  = errors.New("invalid identity key pair")
)

// KeyPair holds an Ed25519 identity in memory. It is created by GenerateKeyPair
// or RestoreKeyPair. Seed exports the profile secret; OnionServiceSeed derives
// a separate Tor service secret. Neither should be logged or shared.
type KeyPair struct {
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
}

// GenerateKeyPair creates fresh keys using the standard library's secure random
// source. It does not read or write profile files.
func GenerateKeyPair() (KeyPair, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate identity keys: %w", err)
	}
	return KeyPair{publicKey: publicKey, privateKey: privateKey}, nil
}

// RestoreKeyPair reconstructs the same identity from a 32-byte Ed25519 seed.
// The returned keys do not share memory with the caller's seed.
func RestoreKeyPair(seed []byte) (KeyPair, error) {
	if len(seed) != ed25519.SeedSize {
		return KeyPair{}, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidSeedSize, len(seed), ed25519.SeedSize)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return KeyPair{publicKey: publicKey, privateKey: privateKey}, nil
}

// PublicKey returns a copy so callers cannot modify the stored key.
func (k KeyPair) PublicKey() ed25519.PublicKey {
	return bytes.Clone(k.publicKey)
}

// Seed returns an independent copy of the secret needed to restore this identity.
// Treat it as a private key: never log it or include it in invitations.
// An uninitialized KeyPair returns ErrInvalidKeyPair.
func (k KeyPair) Seed() ([]byte, error) {
	if len(k.privateKey) != ed25519.PrivateKeySize {
		return nil, ErrInvalidKeyPair
	}
	return k.privateKey.Seed(), nil
}
