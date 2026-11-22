package identity

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"testing"
)

func TestOnionServiceSeedIsStableAndSeparate(t *testing.T) {
	seed := make([]byte, 32)
	keys, err := RestoreKeyPair(seed)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := keys.OnionServiceSeed()
	if err != nil {
		t.Fatal(err)
	}
	// Independent HKDF-SHA256 vector, generated with Python hmac/hashlib.
	const expected = "9fedea73185bf2052784bce24eff1a2111f3a3bc8df88a0d70ea81c605e378c1"
	if hex.EncodeToString(derived) != expected {
		t.Fatalf("onion derivation changed: %x", derived)
	}
	if bytes.Equal(derived, seed) || bytes.Equal(ed25519.NewKeyFromSeed(derived).Public().(ed25519.PublicKey), keys.PublicKey()) {
		t.Fatal("Tor and profile keys must be separate")
	}
	clear(derived)
	restored, _ := RestoreKeyPair(seed)
	again, err := restored.OnionServiceSeed()
	if err != nil || hex.EncodeToString(again) != expected {
		t.Fatal("restoration must preserve onion identity; returned seed must be a copy")
	}
	seed[0] = 1
	other, _ := RestoreKeyPair(seed)
	otherSeed, _ := other.OnionServiceSeed()
	if bytes.Equal(otherSeed, again) {
		t.Fatal("different profiles share an onion key")
	}
}

func TestOnionServiceSeedRejectsInvalidKeys(t *testing.T) {
	if seed, err := (KeyPair{}).OnionServiceSeed(); seed != nil || !errors.Is(err, ErrInvalidKeyPair) {
		t.Fatalf("zero pair: %x, %v", seed, err)
	}
	keys, _ := GenerateKeyPair()
	keys.publicKey[0] ^= 1
	if seed, err := keys.OnionServiceSeed(); seed != nil || !errors.Is(err, ErrInvalidKeyPair) {
		t.Fatalf("inconsistent pair: %x, %v", seed, err)
	}
}
