package app_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profiledir"
	"github.com/bacrehclb/ShadowTalk/internal/storage/sqlite"
)

func TestHistoryOpensWithPersistedProfileAndClearsFactoryKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	keyring := newMemoryKeys()
	created, err := app.NewProfiles(profiledir.New(dir), keyring).Create("Alice")
	if err != nil {
		t.Fatal(err)
	}
	wantKey, err := created.Keys.HistoryKey()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(wantKey)
	var borrowedKey []byte
	open := func() history.Store {
		t.Helper()
		profiles := app.NewProfiles(profiledir.New(dir), keyring)
		s, err := app.NewHistory(profiles, func(ctx context.Context, owner [32]byte, key []byte) (history.Store, error) {
			borrowedKey = key
			if owner != created.Profile.PublicKey || !bytes.Equal(key, wantKey) {
				t.Fatal("factory received the wrong profile or key")
			}
			return sqlite.Open(ctx, dir, owner, key)
		}).Open(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
		if !bytes.Equal(borrowedKey, make([]byte, 32)) {
			t.Fatal("factory key not cleared")
		}
		return s
	}
	s := open()
	m := history.Message{Peer: [32]byte{2}, Direction: history.Incoming, ID: protocol.MessageID{1}, Text: "persisted profile history", RecordedAt: time.Now()}
	want, added, err := s.Append(t.Context(), m)
	if err != nil || !added {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	got, err := s.Get(t.Context(), m.Peer, m.Direction, m.ID)
	if err != nil || got != want {
		t.Fatal("profile reload could not recover history", err)
	}
	if len(keyring.entries) != 1 {
		t.Fatal("history created an unrelated OS key")
	}
}

func TestHistoryRejectsInvalidProfileBeforeOpeningStorage(t *testing.T) {
	keys, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing", "legacy", "mismatch", "invalid-keys", "canceled-before", "canceled-during"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			loaded := app.LoadedProfile{Profile: identity.Profile{Nickname: "Alice", PublicKey: [32]byte(keys.PublicKey()), CreatedAt: time.Now()}, Keys: keys}
			want := error(context.Canceled)
			switch mode {
			case "missing":
				want = identity.ErrProfileNotFound
			case "legacy":
				loaded.Profile.PublicKey = [32]byte{}
				want = identity.ErrInvalidKeyPair
			case "mismatch":
				loaded.Profile.PublicKey[0] ^= 1
				want = app.ErrIdentityMismatch
			case "invalid-keys":
				loaded.Keys = identity.KeyPair{}
				want = app.ErrIdentityMismatch
			case "canceled-before":
				cancel()
			}
			service := app.NewHistory(loadProfileFunc(func() (app.LoadedProfile, error) {
				if mode == "canceled-before" {
					t.Fatal("canceled open loaded the profile")
				}
				if mode == "missing" {
					return app.LoadedProfile{}, want
				}
				if mode == "canceled-during" {
					cancel()
				}
				return loaded, nil
			}), func(context.Context, [32]byte, []byte) (history.Store, error) {
				t.Fatal("invalid profile reached storage")
				return nil, nil
			})
			if s, err := service.Open(ctx); s != nil || !errors.Is(err, want) {
				t.Fatal("unexpected startup result", err)
			}
		})
	}
}

func TestHistoryClearsKeyWhenFactoryFails(t *testing.T) {
	keys, _ := identity.GenerateKeyPair()
	loaded := app.LoadedProfile{Profile: identity.Profile{PublicKey: [32]byte(keys.PublicKey())}, Keys: keys}
	var borrowed []byte
	failure := errors.New("storage unavailable")
	service := app.NewHistory(loadProfileFunc(func() (app.LoadedProfile, error) { return loaded, nil }), func(_ context.Context, _ [32]byte, key []byte) (history.Store, error) {
		borrowed = key
		return nil, failure
	})
	if s, err := service.Open(t.Context()); s != nil || !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if len(borrowed) != 32 || !bytes.Equal(borrowed, make([]byte, 32)) {
		t.Fatal("failed factory retained the borrowed key")
	}
	if _, err := (*app.History)(nil).Open(t.Context()); !errors.Is(err, history.ErrInvalidConfig) {
		t.Fatal(err)
	}
}
