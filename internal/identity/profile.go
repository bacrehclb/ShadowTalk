package identity

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var (
	ErrProfileNotFound       = errors.New("profile not found")
	ErrProfileExists         = errors.New("profile already exists")
	ErrInvalidProfile        = errors.New("invalid profile")
	ErrEncryptionKeyNotFound = errors.New("encryption key not found in system key store")
)

// Profile contains metadata and the public identity, never private key material.
// Its representation is independent of the storage format and the interface.
type Profile struct {
	Nickname  string
	CreatedAt time.Time
	PublicKey [ed25519.PublicKeySize]byte
}

// HasIdentity is false for legacy metadata-only profiles.
func (p Profile) HasIdentity() bool { return p.PublicKey != [ed25519.PublicKeySize]byte{} }

// Fingerprint is a full SHA-256 digest of the Ed25519 public key.
func (p Profile) Fingerprint() string {
	return PublicKeyFingerprint(p.PublicKey)
}

// PublicKeyFingerprint is shared by profiles, invitations and pinned contacts.
func PublicKeyFingerprint(publicKey [ed25519.PublicKeySize]byte) string {
	if publicKey == [ed25519.PublicKeySize]byte{} {
		return ""
	}
	digest := sha256.Sum256(publicKey[:])
	return "SHA256:" + hex.EncodeToString(digest[:])
}

func (p Profile) Validate() error {
	if err := ValidateNickname(p.Nickname); err != nil {
		return err
	}
	if p.CreatedAt.IsZero() || p.CreatedAt.Year() < 1 || p.CreatedAt.Year() > 9999 {
		return fmt.Errorf("%w: invalid creation time", ErrInvalidProfile)
	}
	return nil
}
