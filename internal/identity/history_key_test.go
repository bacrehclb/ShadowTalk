package identity

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

func TestHistoryKeyIsStableAndSeparate(t *testing.T) {
	seed := make([]byte, 32)
	keys, err := RestoreKeyPair(seed)
	if err != nil {
		t.Fatal(err)
	}
	key, err := keys.HistoryKey()
	if err != nil {
		t.Fatal(err)
	}
	// Independent HKDF-SHA256 vector generated with Python hmac/hashlib.
	const want = "06580d59a425b5c189437e1ac78c245fcf6bd639e44155bc02ae505917eefdff"
	if hex.EncodeToString(key) != want {
		t.Fatal("persistent history derivation changed")
	}
	onion, err := keys.OnionServiceSeed()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(key, seed) || bytes.Equal(key, onion) {
		t.Fatal("history shares a key with another purpose")
	}
	clear(key)
	restored, _ := RestoreKeyPair(seed)
	again, err := restored.HistoryKey()
	if err != nil || hex.EncodeToString(again) != want {
		t.Fatal("restore or caller mutation changed the history key", err)
	}
	seed[0] = 1
	other, _ := RestoreKeyPair(seed)
	otherKey, _ := other.HistoryKey()
	if bytes.Equal(otherKey, again) {
		t.Fatal("different identities share a history key")
	}
}

func TestHistoryKeyRejectsInvalidKeys(t *testing.T) {
	if key, err := (KeyPair{}).HistoryKey(); key != nil || !errors.Is(err, ErrInvalidKeyPair) {
		t.Fatal("zero key pair accepted", err)
	}
	keys, _ := GenerateKeyPair()
	keys.publicKey[0] ^= 1
	if key, err := keys.HistoryKey(); key != nil || !errors.Is(err, ErrInvalidKeyPair) {
		t.Fatal("inconsistent key pair accepted", err)
	}
}
