package identity

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
)

func TestSeedRestoresOriginalIdentity(t *testing.T) {
	original, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	seed, err := original.Seed()
	if err != nil {
		t.Fatal(err)
	}
	if len(seed) != ed25519.SeedSize {
		t.Fatal("exported seed has an unexpected size")
	}
	restored, err := RestoreKeyPair(seed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.PublicKey(), original.PublicKey()) || !bytes.Equal(restored.privateKey, original.privateKey) {
		t.Fatal("restored keys differ from the original identity")
	}
	message := []byte("shadowtalk restored identity test")
	if !ed25519.Verify(original.PublicKey(), message, ed25519.Sign(restored.privateKey, message)) {
		t.Fatal("restored identity cannot authenticate with the original public key")
	}
}

func TestSeedReturnsIndependentCopy(t *testing.T) {
	keys, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	expectedPrivateKey := bytes.Clone(keys.privateKey)
	seed, err := keys.Seed()
	if err != nil {
		t.Fatal(err)
	}
	seed[0] ^= 0xff
	if !bytes.Equal(keys.privateKey, expectedPrivateKey) {
		t.Fatal("modifying the exported seed changed the private key")
	}
}

func TestRestoreKeyPairDoesNotRetainCallerSeed(t *testing.T) {
	seed := bytes.Repeat([]byte{0x42}, ed25519.SeedSize)
	expectedSeed := bytes.Clone(seed)
	keys, err := RestoreKeyPair(seed)
	if err != nil {
		t.Fatal(err)
	}
	clear(seed)
	gotSeed, err := keys.Seed()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotSeed, expectedSeed) {
		t.Fatal("modifying the input seed changed the restored identity")
	}
}

func TestRestoreKeyPairRejectsInvalidSeedSize(t *testing.T) {
	tests := []struct {
		name string
		seed []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"too short", make([]byte, ed25519.SeedSize-1)},
		{"too long", make([]byte, ed25519.SeedSize+1)},
		{"private key length", make([]byte, ed25519.PrivateKeySize)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys, err := RestoreKeyPair(tt.seed)
			if !errors.Is(err, ErrInvalidSeedSize) {
				t.Fatalf("got %v, want ErrInvalidSeedSize", err)
			}
			if len(keys.PublicKey()) != 0 || len(keys.privateKey) != 0 {
				t.Fatal("invalid seed produced key material")
			}
		})
	}
}

func TestUninitializedKeyPairCannotExportSeed(t *testing.T) {
	var keys KeyPair
	seed, err := keys.Seed()
	if !errors.Is(err, ErrInvalidKeyPair) || seed != nil {
		t.Fatal("uninitialized key pair must return an error without seed material")
	}
}
