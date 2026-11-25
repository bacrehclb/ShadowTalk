package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"errors"
)

// SeedEncryptionKeySize is the size of the separate AES-256 encryption key.
const SeedEncryptionKeySize = 32

const seedEncryptionContext = "ShadowTalk/identity-seed/v1"

var (
	ErrInvalidEncryptionKeySize = errors.New("seed encryption key must contain 32 bytes")
	ErrSeedDecryption           = errors.New("cannot decrypt identity seed")
)

// EncryptSeed encrypts a 32-byte Ed25519 seed using AES-256-GCM. The caller must
// supply a separate, cryptographically random key, not a password or the seed.
// The result contains a fresh random nonce, ciphertext and authentication tag.
// No more than 2^32 encryptions may use the same key.
func EncryptSeed(seed, key []byte) ([]byte, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, ErrInvalidSeedSize
	}
	aead, err := newSeedCipher(key)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nil, nil, seed, []byte(seedEncryptionContext)), nil
}

// DecryptSeed returns a new slice containing the secret seed. Invalid lengths,
// a wrong key or modified data return an error without any plaintext.
// Neither encryption nor decryption modifies the caller's input slices.
func DecryptSeed(encryptedSeed, key []byte) ([]byte, error) {
	aead, err := newSeedCipher(key)
	if err != nil {
		return nil, err
	}
	if len(encryptedSeed) != ed25519.SeedSize+aead.Overhead() {
		return nil, ErrSeedDecryption
	}
	seed, err := aead.Open(nil, nil, encryptedSeed, []byte(seedEncryptionContext))
	if err != nil {
		return nil, ErrSeedDecryption
	}
	return seed, nil
}

func newSeedCipher(key []byte) (cipher.AEAD, error) {
	if len(key) != SeedEncryptionKeySize {
		return nil, ErrInvalidEncryptionKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}
