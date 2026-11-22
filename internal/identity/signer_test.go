package identity

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

func TestSignerUsesRestoredIdentityWithoutExportingPrivateKey(t *testing.T) {
	keys, err := RestoreKeyPair(bytes.Repeat([]byte{42}, ed25519.SeedSize))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := keys.Signer()
	if err != nil {
		t.Fatal(err)
	}
	if _, exportsBytes := signer.(ed25519.PrivateKey); exportsBytes {
		t.Fatal("signer exposes private key bytes")
	}
	message := []byte("ShadowTalk signature test")
	signature, err := signer.Sign(rand.Reader, message, crypto.Hash(0))
	if err != nil || !ed25519.Verify(keys.PublicKey(), message, signature) {
		t.Fatal("signature does not match identity", err)
	}
	public := signer.Public().(ed25519.PublicKey)
	public[0] ^= 0xff
	if !bytes.Equal(signer.Public().(ed25519.PublicKey), keys.PublicKey()) {
		t.Fatal("signer's public key aliases caller memory")
	}
	if _, err := signer.Sign(rand.Reader, message, nil); err == nil {
		t.Fatal("nil signing options accepted")
	}
	if _, err := signer.Sign(rand.Reader, message, crypto.SHA256); err == nil {
		t.Fatal("unsupported signing options accepted")
	}
}

func TestInvalidKeyPairCannotSign(t *testing.T) {
	signer, err := (KeyPair{}).Signer()
	if signer != nil || !errors.Is(err, ErrInvalidKeyPair) {
		t.Fatal("zero key pair accepted")
	}
	keys, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	keys.publicKey[0] ^= 0xff
	signer, err = keys.Signer()
	if signer != nil || !errors.Is(err, ErrInvalidKeyPair) {
		t.Fatal("inconsistent key pair accepted")
	}
}
