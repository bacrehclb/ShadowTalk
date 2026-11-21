package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
)

func TestSeedEncryptionRoundTripPreservesInputs(t *testing.T) {
	seed, key := seedEncryptionInputs(t)
	originalSeed, originalKey := bytes.Clone(seed), bytes.Clone(key)
	encrypted, err := EncryptSeed(seed, key)
	if err != nil {
		t.Fatal(err)
	}
	originalEncrypted := bytes.Clone(encrypted)
	decrypted, err := DecryptSeed(encrypted, key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, originalSeed) {
		t.Fatal("decryption did not restore the original seed")
	}
	clear(decrypted)
	if !bytes.Equal(seed, originalSeed) || !bytes.Equal(key, originalKey) || !bytes.Equal(encrypted, originalEncrypted) {
		t.Fatal("encryption or decryption modified caller-owned data")
	}
}

func TestSeedEncryptionUsesFreshNonce(t *testing.T) {
	seed, key := seedEncryptionInputs(t)
	first, err := EncryptSeed(seed, key)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncryptSeed(seed, key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("encrypting the same seed twice reused the nonce")
	}
	for _, encrypted := range [][]byte{first, second} {
		got, err := DecryptSeed(encrypted, key)
		if err != nil || !bytes.Equal(got, seed) {
			t.Fatal("an independently encrypted seed cannot be decrypted")
		}
	}
}

func TestSeedDecryptionRejectsWrongKeyAndDamagedData(t *testing.T) {
	seed, key := seedEncryptionInputs(t)
	encrypted, err := EncryptSeed(seed, key)
	if err != nil {
		t.Fatal(err)
	}
	wrongKey := bytes.Clone(key)
	wrongKey[0] ^= 0xff
	aead, err := newSeedCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	otherPurpose := aead.Seal(nil, nil, seed, []byte("another purpose"))
	type decryptCase struct {
		name string
		data []byte
		key  []byte
	}
	tests := []decryptCase{
		{"wrong key", encrypted, wrongKey},
		{"different purpose", otherPurpose, key},
		{"nil data", nil, key},
		{"truncated data", encrypted[:len(encrypted)-1], key},
		{"appended data", append(bytes.Clone(encrypted), 0), key},
	}
	// Cover modifications to the nonce, encrypted seed and authentication tag.
	for i := range encrypted {
		damaged := bytes.Clone(encrypted)
		damaged[i] ^= 0x01
		tests = append(tests, decryptCase{fmt.Sprintf("modified byte %d", i), damaged, key})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecryptSeed(tt.data, tt.key)
			if !errors.Is(err, ErrSeedDecryption) || got != nil {
				t.Fatal("invalid encrypted seed must fail without returning plaintext")
			}
		})
	}
}

func TestSeedEncryptionRejectsInvalidInputSizes(t *testing.T) {
	seed, key := seedEncryptionInputs(t)
	for _, size := range []int{0, 1, ed25519.SeedSize - 1, ed25519.SeedSize + 1, ed25519.PrivateKeySize} {
		t.Run(fmt.Sprintf("seed size %d", size), func(t *testing.T) {
			got, err := EncryptSeed(make([]byte, size), key)
			if !errors.Is(err, ErrInvalidSeedSize) || got != nil {
				t.Fatal("invalid seed size must fail without producing ciphertext")
			}
		})
	}
	for _, size := range []int{0, 16, 24, SeedEncryptionKeySize - 1, SeedEncryptionKeySize + 1} {
		t.Run(fmt.Sprintf("key size %d", size), func(t *testing.T) {
			invalidKey := make([]byte, size)
			if got, err := EncryptSeed(seed, invalidKey); !errors.Is(err, ErrInvalidEncryptionKeySize) || got != nil {
				t.Fatal("encryption accepted an invalid key size")
			}
			if got, err := DecryptSeed(nil, invalidKey); !errors.Is(err, ErrInvalidEncryptionKeySize) || got != nil {
				t.Fatal("decryption accepted an invalid key size")
			}
		})
	}
}

func seedEncryptionInputs(t *testing.T) (seed, key []byte) {
	t.Helper()
	seed = bytes.Repeat([]byte{0x42}, ed25519.SeedSize)
	key = make([]byte, SeedEncryptionKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return seed, key
}
