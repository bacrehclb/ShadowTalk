package identity

import (
	"bytes"
	"crypto/ed25519"
	"testing"
)

func TestGeneratedKeysFormValidPair(t *testing.T) {
	keys, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	publicKey := keys.PublicKey()
	if len(publicKey) != ed25519.PublicKeySize || len(keys.privateKey) != ed25519.PrivateKeySize {
		t.Fatal("generated keys have unexpected sizes")
	}
	message := []byte("shadowtalk identity test")
	signature := ed25519.Sign(keys.privateKey, message)
	if !ed25519.Verify(publicKey, message, signature) {
		t.Fatal("public key does not verify a signature from its private key")
	}
}

func TestGenerateKeyPairCreatesIndependentKeys(t *testing.T) {
	first, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.PublicKey(), second.PublicKey()) || bytes.Equal(first.privateKey, second.privateKey) {
		t.Fatal("separate calls reused key material")
	}
}

func TestPublicKeyReturnsIndependentCopy(t *testing.T) {
	keys, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	expected := bytes.Clone(keys.PublicKey())
	returnedKey := keys.PublicKey()
	returnedKey[0] ^= 0xff
	if !bytes.Equal(keys.PublicKey(), expected) {
		t.Fatal("modifying the returned public key changed the stored key")
	}
}
