package keyring

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	oskeyring "github.com/zalando/go-keyring"
)

func TestDeleteOnlyRemovesAddressedEntry(t *testing.T) {
	const namespace = "ShadowTalk/seed-encryption/v1"
	entries := map[[2]string]string{
		{namespace, "key-1"}:       "malformed entry can still be deleted",
		{namespace, "key-2"}:       "other key",
		{"other-service", "key-1"}: "other application",
	}
	store := &Store{
		remove: func(service, account string) error {
			address := [2]string{service, account}
			if _, ok := entries[address]; !ok {
				return oskeyring.ErrNotFound
			}
			delete(entries, address)
			return nil
		},
		get: func(service, account string) (string, error) {
			value, ok := entries[[2]string{service, account}]
			if !ok {
				return "", fmt.Errorf("lookup: %w", oskeyring.ErrNotFound)
			}
			return value, nil
		},
	}
	for range 2 {
		if err := store.Delete("key-1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := entries[[2]string{namespace, "key-1"}]; ok {
		t.Fatal("addressed entry was not deleted")
	}
	if len(entries) != 2 || entries[[2]string{namespace, "key-2"}] != "other key" || entries[[2]string{"other-service", "key-1"}] != "other application" {
		t.Fatal("Delete modified another entry")
	}
}

func TestDeleteHandlesBackendErrorsWithoutReadingSecrets(t *testing.T) {
	const secret = "sensitive-backend-output"
	for _, tt := range []struct {
		name string
		err  error
		want error
	}{
		{"missing", oskeyring.ErrNotFound, nil},
		{"wrapped missing", fmt.Errorf("%s: %w", secret, oskeyring.ErrNotFound), nil},
		{"access failure", errors.New(secret), ErrUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// A nil get function catches accidental reads after a definitive
			// missing-entry response or a failed deletion.
			store := &Store{remove: func(string, string) error { return tt.err }}
			err := store.Delete("key-1")
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			if strings.Contains(fmt.Sprintf("%+v", err), secret) {
				t.Fatal("deletion error exposed backend output")
			}
		})
	}
}

func TestDeleteRejectsUnconfirmedRemoval(t *testing.T) {
	const secret = "sensitive-backend-output"
	for _, tt := range []struct {
		name        string
		value       string
		readErr     error
		unavailable bool
	}{
		{"entry remains", strings.Repeat("A", 43) + "=", nil, false},
		{"empty entry remains", "", nil, false},
		{"malformed entry remains", secret, nil, false},
		{"read denied", secret, errors.New(secret), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &Store{
				remove: func(string, string) error { return nil },
				get: func(service, account string) (string, error) {
					if service != "ShadowTalk/seed-encryption/v1" || account != "key-1" {
						t.Fatal("verification addressed a different entry")
					}
					return tt.value, tt.readErr
				},
			}
			err := store.Delete("key-1")
			if !errors.Is(err, ErrDeleteUnconfirmed) {
				t.Fatalf("got %v, want ErrDeleteUnconfirmed", err)
			}
			if errors.Is(err, ErrUnavailable) != tt.unavailable {
				t.Fatal("verification error was incorrectly classified")
			}
			if strings.Contains(fmt.Sprintf("%+v", err), secret) {
				t.Fatal("verification error exposed secret data or backend output")
			}
		})
	}
}
