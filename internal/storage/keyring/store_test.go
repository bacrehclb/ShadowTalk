package keyring

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
	oskeyring "github.com/zalando/go-keyring"
)

func TestSetAndGetIndependentKeys(t *testing.T) {
	entries := make(map[string]string)
	store := &Store{
		set: func(service, account, secret string) error {
			if service != "ShadowTalk/seed-encryption/v1" {
				t.Fatal("unexpected service namespace")
			}
			entries[service+":"+account] = secret
			return nil
		},
		get: func(service, account string) (string, error) {
			secret, ok := entries[service+":"+account]
			if !ok {
				return "", oskeyring.ErrNotFound
			}
			return secret, nil
		},
	}
	first := bytes.Repeat([]byte{0xA1}, identity.SeedEncryptionKeySize)
	second := bytes.Repeat([]byte{0xB2}, identity.SeedEncryptionKeySize)
	for id, key := range map[string][]byte{"key_A-1": first, strings.Repeat("b", 128): second} {
		if err := store.Set(id, key); err != nil {
			t.Fatal(err)
		}
	}
	// Set must not retain the caller's slice; Get must return an independent copy.
	first[0] ^= 0xFF
	loaded, err := store.Get("key_A-1")
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte{0xA1}, identity.SeedEncryptionKeySize)
	if !bytes.Equal(loaded, want) {
		t.Fatal("stored key changed with caller's input")
	}
	loaded[0] ^= 0xFF
	loaded, err = store.Get("key_A-1")
	if err != nil || !bytes.Equal(loaded, want) {
		t.Fatal("stored key changed with returned slice")
	}
	if err := store.Set("key_A-1", first); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Get("key_A-1")
	if err != nil || !bytes.Equal(loaded, first) {
		t.Fatal("Set did not replace the addressed entry")
	}
	loaded, err = store.Get(strings.Repeat("b", 128))
	if err != nil || !bytes.Equal(loaded, second) {
		t.Fatal("Set changed another entry")
	}
	loaded, err = store.Get("missing")
	if loaded != nil || !errors.Is(err, ErrNotFound) || len(entries) != 2 {
		t.Fatal("missing entry was not reported without creating a key")
	}
}

func TestInvalidInputDoesNotAccessSystemStore(t *testing.T) {
	// Nil functions deliberately fail the test if validation attempts any I/O.
	store := &Store{}
	key := make([]byte, identity.SeedEncryptionKeySize)
	for _, id := range []string{"", strings.Repeat("a", 129), "ivan name", "ivan\nname", "ivan\x00name", "иван", "a:b", "../a"} {
		if err := store.Set(id, key); !errors.Is(err, ErrInvalidKeyID) {
			t.Fatalf("Set returned %v, want ErrInvalidKeyID", err)
		}
		if got, err := store.Get(id); got != nil || !errors.Is(err, ErrInvalidKeyID) {
			t.Fatalf("Get returned %v, want ErrInvalidKeyID and no key", err)
		}
		if err := store.Delete(id); !errors.Is(err, ErrInvalidKeyID) {
			t.Fatalf("Delete returned %v, want ErrInvalidKeyID", err)
		}
	}
	for _, size := range []int{0, 16, 24, 31, 33, 64} {
		if err := store.Set("key-1", make([]byte, size)); !errors.Is(err, identity.ErrInvalidEncryptionKeySize) {
			t.Fatalf("size %d: got %v, want ErrInvalidEncryptionKeySize", size, err)
		}
	}
}

func TestGetRejectsMalformedEntries(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString(make([]byte, identity.SeedEncryptionKeySize))
	for name, encoded := range map[string]string{
		"empty":            "",
		"invalid base64":   strings.Repeat("!", len(valid)),
		"short key":        base64.StdEncoding.EncodeToString(make([]byte, 31)),
		"long key":         base64.StdEncoding.EncodeToString(make([]byte, 33)),
		"oversized":        strings.Repeat("A", 4096),
		"nonzero pad bits": valid[:42] + "B=",
		"line break":       valid[:42] + "\n=",
	} {
		t.Run(name, func(t *testing.T) {
			store := &Store{get: func(string, string) (string, error) { return encoded, nil }}
			key, err := store.Get("key-1")
			if key != nil || !errors.Is(err, ErrInvalidStoredKey) {
				t.Fatalf("got %v, want ErrInvalidStoredKey and no key", err)
			}
		})
	}
}

func TestBackendErrorsDoNotExposeSecrets(t *testing.T) {
	const secret = "sensitive-backend-output"
	backendErr := errors.New(secret)
	store := &Store{
		set: func(string, string, string) error { return backendErr },
		get: func(string, string) (string, error) { return secret, backendErr },
	}
	setErr := store.Set("key-1", make([]byte, identity.SeedEncryptionKeySize))
	key, getErr := store.Get("key-1")
	if key != nil {
		t.Fatal("Get returned data on backend failure")
	}
	for _, err := range []error{setErr, getErr} {
		if !errors.Is(err, ErrUnavailable) || strings.Contains(fmt.Sprintf("%+v", err), secret) {
			t.Fatal("backend error was not safely classified")
		}
		if errors.Is(err, ErrNotFound) {
			t.Fatal("backend failure was reported as a missing key")
		}
	}
	store.get = func(string, string) (string, error) {
		return secret, fmt.Errorf("%s: %w", secret, oskeyring.ErrNotFound)
	}
	key, err := store.Get("key-1")
	if key != nil || !errors.Is(err, ErrNotFound) || strings.Contains(fmt.Sprint(err), secret) {
		t.Fatal("wrapped missing-entry error was not safely classified")
	}
}

func TestSetVerifiesStoredKey(t *testing.T) {
	key := make([]byte, identity.SeedEncryptionKeySize)
	for _, tt := range []struct {
		name    string
		encoded string
		err     error
		cause   error
	}{
		{"missing", "", oskeyring.ErrNotFound, ErrNotFound},
		{"unavailable", "", errors.New("access denied"), ErrUnavailable},
		{"malformed", "broken", nil, ErrInvalidStoredKey},
		{"different key", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, len(key))), nil, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &Store{
				set: func(string, string, string) error { return nil },
				get: func(string, string) (string, error) { return tt.encoded, tt.err },
			}
			err := store.Set("key-1", key)
			if !errors.Is(err, ErrWriteUnconfirmed) {
				t.Fatalf("got %v, want ErrWriteUnconfirmed", err)
			}
			if tt.cause != nil && !errors.Is(err, tt.cause) {
				t.Fatalf("got %v, want cause %v", err, tt.cause)
			}
		})
	}
}
