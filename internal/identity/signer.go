package identity

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"errors"
	"io"
)

// Signer grants signing capability without exporting private key bytes. It is
// safe for concurrent use and uses the existing identity, not a new key pair.
func (k KeyPair) Signer() (crypto.Signer, error) {
	if len(k.privateKey) != ed25519.PrivateKeySize || len(k.publicKey) != ed25519.PublicKeySize ||
		!bytes.Equal(k.privateKey.Public().(ed25519.PublicKey), k.publicKey) {
		return nil, ErrInvalidKeyPair
	}
	return identitySigner{privateKey: k.privateKey}, nil
}

type identitySigner struct{ privateKey ed25519.PrivateKey }

func (s identitySigner) Public() crypto.PublicKey { return s.privateKey.Public() }

func (s identitySigner) Sign(random io.Reader, message []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts == nil {
		return nil, errors.New("Ed25519 signing options are required")
	}
	return s.privateKey.Sign(random, message, opts)
}
