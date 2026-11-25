package app_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/storage/keyring"
	"github.com/bacrehclb/ShadowTalk/internal/storage/seedfile"
)

// Check that the production adapters satisfy the read-only contracts without
// accessing the real system key store during tests.
var (
	_ app.EncryptedSeedReader = (*seedfile.Store)(nil)
	_ app.EncryptionKeyReader = (*keyring.Store)(nil)
)

func TestIdentityLoaderRestoresSavedIdentity(t *testing.T) {
	original, key, encrypted := encryptedIdentity(t)
	directory := filepath.Join(t.TempDir(), "профиль с пробелами")
	if err := seedfile.New(directory).Create("saved-key_1", encrypted); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "identity-seed.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var returnedKey []byte
	keyReads := 0
	keys := keyReaderFunc(func(id string) ([]byte, error) {
		if id != "saved-key_1" {
			t.Fatal("requested key does not match the saved key ID")
		}
		keyReads++
		returnedKey = bytes.Clone(key)
		return returnedKey, nil
	})
	loader := app.NewIdentityLoader(seedfile.New(directory), keys)
	for range 2 {
		loaded, err := loader.Load()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(loaded.PublicKey(), original.PublicKey()) {
			t.Fatal("loader restored a different public key")
		}
		wantSeed, err := original.Seed()
		if err != nil {
			t.Fatal(err)
		}
		gotSeed, err := loaded.Seed()
		if err != nil || !bytes.Equal(gotSeed, wantSeed) {
			t.Fatal("restored private key is missing or was erased")
		}
		assertCleared(t, returnedKey)
	}
	if keyReads != 2 {
		t.Fatal("Load did not read the encryption key on each call")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("Load modified the saved seed file")
	}
}

func TestIdentityLoaderStopsBeforeKeyAccessOnSeedFailure(t *testing.T) {
	for _, want := range []error{seedfile.ErrNotFound, seedfile.ErrInvalidRecord, seedfile.ErrUnsupportedVersion, fs.ErrPermission} {
		t.Run(want.Error(), func(t *testing.T) {
			seeds := seedReaderFunc(func() (string, []byte, error) {
				return "ignored-key", nil, fmt.Errorf("read failed: %w", want)
			})
			loader := app.NewIdentityLoader(seeds, unexpectedKeyReader(t))
			got, err := loader.Load()
			assertLoadFailure(t, got, err, want)
		})
	}
	for _, id := range []string{"", "../key", "key with spaces"} {
		t.Run("invalid ID "+id, func(t *testing.T) {
			seeds := seedReaderFunc(func() (string, []byte, error) { return id, nil, nil })
			got, err := app.NewIdentityLoader(seeds, unexpectedKeyReader(t)).Load()
			assertLoadFailure(t, got, err, identity.ErrInvalidEncryptionKeyID)
		})
	}
}

func TestIdentityLoaderPropagatesKeyFailures(t *testing.T) {
	_, key, encrypted := encryptedIdentity(t)
	for _, want := range []error{keyring.ErrNotFound, keyring.ErrUnavailable, keyring.ErrInvalidStoredKey} {
		t.Run(want.Error(), func(t *testing.T) {
			seeds := seedReaderFunc(func() (string, []byte, error) { return "key-1", encrypted, nil })
			returnedKey := bytes.Clone(key)
			keys := keyReaderFunc(func(string) ([]byte, error) {
				return returnedKey, fmt.Errorf("get failed: %w", want)
			})
			got, err := app.NewIdentityLoader(seeds, keys).Load()
			assertLoadFailure(t, got, err, want)
			assertCleared(t, returnedKey)
		})
	}
}

func TestIdentityLoaderRejectsWrongKeyAndDamagedCiphertext(t *testing.T) {
	_, key, encrypted := encryptedIdentity(t)
	wrongKey := bytes.Clone(key)
	wrongKey[0] ^= 1
	damaged := bytes.Clone(encrypted)
	damaged[len(damaged)-1] ^= 1
	for _, tt := range []struct {
		name      string
		key, data []byte
		want      error
	}{
		{"wrong key", wrongKey, encrypted, identity.ErrSeedDecryption},
		{"damaged ciphertext", key, damaged, identity.ErrSeedDecryption},
		{"truncated ciphertext", key, encrypted[:len(encrypted)-1], identity.ErrSeedDecryption},
		{"missing ciphertext", key, nil, identity.ErrSeedDecryption},
		{"missing key", nil, encrypted, identity.ErrInvalidEncryptionKeySize},
		{"short key", key[:len(key)-1], encrypted, identity.ErrInvalidEncryptionKeySize},
	} {
		t.Run(tt.name, func(t *testing.T) {
			seeds := seedReaderFunc(func() (string, []byte, error) { return "key-1", tt.data, nil })
			returnedKey := bytes.Clone(tt.key)
			keys := keyReaderFunc(func(string) ([]byte, error) { return returnedKey, nil })
			got, err := app.NewIdentityLoader(seeds, keys).Load()
			assertLoadFailure(t, got, err, tt.want)
			assertCleared(t, returnedKey)
		})
	}
}

type seedReaderFunc func() (string, []byte, error)

func (f seedReaderFunc) Load() (string, []byte, error) { return f() }

type keyReaderFunc func(string) ([]byte, error)

func (f keyReaderFunc) Get(id string) ([]byte, error) { return f(id) }

func unexpectedKeyReader(t *testing.T) keyReaderFunc {
	t.Helper()
	return func(string) ([]byte, error) {
		t.Fatal("key store accessed after a seed read or validation failure")
		return nil, nil
	}
}

func assertLoadFailure(t *testing.T, got identity.KeyPair, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
	seed, seedErr := got.Seed()
	if len(got.PublicKey()) != 0 || seed != nil || !errors.Is(seedErr, identity.ErrInvalidKeyPair) {
		t.Fatal("failed load returned an identity")
	}
}

func assertCleared(t *testing.T, key []byte) {
	t.Helper()
	if !bytes.Equal(key, make([]byte, len(key))) {
		t.Fatal("temporary encryption key was not cleared")
	}
}

func encryptedIdentity(t *testing.T) (identity.KeyPair, []byte, []byte) {
	t.Helper()
	pair, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	seed, err := pair.Seed()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(seed)
	key := make([]byte, identity.SeedEncryptionKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	encrypted, err := identity.EncryptSeed(seed, key)
	if err != nil {
		t.Fatal(err)
	}
	return pair, key, encrypted
}
