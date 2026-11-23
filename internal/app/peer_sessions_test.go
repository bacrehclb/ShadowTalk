package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/peers"
	"github.com/bacrehclb/ShadowTalk/internal/storage/contactsfile"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profiledir"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profilefile"
)

type loadProfileFunc func() (app.LoadedProfile, error)

func (f loadProfileFunc) Load() (app.LoadedProfile, error) { return f() }

type readContactsFunc func() (*contacts.Book, error)

func (f readContactsFunc) Read() (*contacts.Book, error) { return f() }

func TestPeerSessionsLoadPersistedIdentityBeforeOpeningTransport(t *testing.T) {
	directory := t.TempDir()
	keyring := newMemoryKeys()
	created, err := app.NewProfiles(profiledir.New(directory), keyring).Create("Alice")
	if err != nil {
		t.Fatal(err)
	}
	profiles := app.NewProfiles(profiledir.New(directory), keyring)
	store := contactsfile.New(directory, created.Profile.PublicKey)
	opened := false
	startupFailure := errors.New("transport unavailable")
	service := app.NewPeerSessions(profiles, store, func(ctx context.Context, keys identity.KeyPair) (peers.Transport, error) {
		opened = true
		if [32]byte(keys.PublicKey()) != created.Profile.PublicKey || ctx.Err() != nil {
			t.Fatal("factory did not receive the persisted identity and live context")
		}
		return nil, startupFailure
	})
	if manager, err := service.Start(t.Context(), peers.Options{}); manager != nil || !errors.Is(err, startupFailure) || !opened {
		t.Fatalf("startup: %v, opened=%v", err, opened)
	}
	reloaded, err := profiles.Load()
	if err != nil || reloaded.Profile != created.Profile || len(keyring.entries) != 1 {
		t.Fatal("failed startup changed the profile")
	}
	if _, err := os.Stat(filepath.Join(directory, "contacts.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("empty contact book was written during startup")
	}
}

func TestPeerSessionsRejectUnavailableLocalStateBeforeOpeningTransport(t *testing.T) {
	for _, mode := range []string{"missing-profile", "legacy-profile", "missing-key", "corrupt-contacts", "foreign-contacts"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			keyring := newMemoryKeys()
			profiles := app.NewProfiles(profiledir.New(directory), keyring)
			var owner [32]byte
			want := identity.ErrProfileNotFound
			if mode == "legacy-profile" {
				want = identity.ErrInvalidKeyPair
				if err := profilefile.New(directory).Create(identity.Profile{Nickname: "Legacy", CreatedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
			} else if mode != "missing-profile" {
				loaded, err := profiles.Create("Alice")
				if err != nil {
					t.Fatal(err)
				}
				owner = loaded.Profile.PublicKey
			}
			switch mode {
			case "missing-key":
				clear(keyring.entries)
				want = identity.ErrEncryptionKeyNotFound
			case "corrupt-contacts":
				want = contactsfile.ErrInvalidRecord
				if err := os.WriteFile(filepath.Join(directory, "contacts.json"), []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
			case "foreign-contacts":
				want = contactsfile.ErrOwnerMismatch
				keys, err := identity.GenerateKeyPair()
				if err != nil {
					t.Fatal(err)
				}
				local, err := profiles.Load()
				if err != nil {
					t.Fatal(err)
				}
				address, err := local.Keys.OnionAddress()
				if err != nil {
					t.Fatal(err)
				}
				if err := contactsfile.New(directory, [32]byte(keys.PublicKey())).Update(func(book *contacts.Book) error {
					_, err := book.Add(contacts.Contact{Alias: "Alice", Nickname: "Alice", PublicKey: owner, Onion: address, AddedAt: time.Now().UTC()})
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			opened := false
			service := app.NewPeerSessions(profiles, contactsfile.New(directory, owner), func(context.Context, identity.KeyPair) (peers.Transport, error) {
				opened = true
				return nil, nil
			})
			if manager, err := service.Start(t.Context(), peers.Options{}); manager != nil || !errors.Is(err, want) || opened {
				t.Fatalf("manager=%v err=%v opened=%v", manager, err, opened)
			}
		})
	}
}

func TestPeerSessionsCheckIdentityAndCancellationBeforeReadingContacts(t *testing.T) {
	keys, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"mismatch", "canceled-during-load"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			profile := identity.Profile{Nickname: "Alice", PublicKey: [32]byte(keys.PublicKey()), CreatedAt: time.Now().UTC()}
			want := error(context.Canceled)
			if mode == "mismatch" {
				profile.PublicKey[0] ^= 0xff
				want = app.ErrIdentityMismatch
			}
			service := app.NewPeerSessions(loadProfileFunc(func() (app.LoadedProfile, error) {
				if mode == "canceled-during-load" {
					cancel()
				}
				return app.LoadedProfile{Profile: profile, Keys: keys}, nil
			}), readContactsFunc(func() (*contacts.Book, error) {
				t.Error("invalid identity or canceled load reached contact storage")
				return nil, nil
			}), func(context.Context, identity.KeyPair) (peers.Transport, error) {
				t.Error("invalid startup reached transport")
				return nil, nil
			})
			if _, err := service.Start(ctx, peers.Options{}); !errors.Is(err, want) {
				t.Fatal(err)
			}
		})
	}
}
