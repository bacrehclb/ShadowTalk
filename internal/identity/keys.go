package identity

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
)

// KeyPair holds an Ed25519 identity in memory. It is created by GenerateKeyPair.
// Private key material is not exposed through exported fields or accessors.
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

// PublicKey returns a copy so callers cannot modify the stored key.
func (k KeyPair) PublicKey() ed25519.PublicKey {
	return bytes.Clone(k.publicKey)
}
