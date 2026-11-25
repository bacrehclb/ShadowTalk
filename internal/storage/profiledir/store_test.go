package profiledir

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profilefile"
)

func samplePending() app.PendingProfile {
	p := app.PendingProfile{Data: app.ProfileData{
		Profile: identity.Profile{Nickname: "test_01", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		KeyID:   "test-key_1", EncryptedSeed: bytes.Repeat([]byte{1}, 60),
	}}
	p.Data.Profile.PublicKey[0] = 1
	return p
}

func TestJournalCommitIsRestartableAndPreservesConflicts(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	release, err := store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	pending := samplePending()
	if err := store.Stage(pending); err != nil {
		t.Fatal(err)
	}
	if err := store.Stage(pending); !errors.Is(err, app.ErrProfilePending) {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, app.ErrProfilePending) {
		t.Fatal(err)
	}
	if err := store.Commit(pending); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(pending); err != nil {
		t.Fatal(err)
	}
	if err := store.Abort(pending); !errors.Is(err, app.ErrProfileConflict) {
		t.Fatal("abort discarded a published identity")
	}
	if err := store.Finish(); err != nil {
		t.Fatal(err)
	}
	saved, err := New(dir).Load()
	if err != nil || !sameData(saved, pending.Data) {
		t.Fatal("journal changed on commit")
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{"profile.json", "identity-seed.json", ".profile.lock"} {
			info, err := os.Stat(filepath.Join(dir, name))
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatal("incorrect file permissions")
			}
		}
	}
}

func TestMalformedJournalIsPreserved(t *testing.T) {
	for _, data := range []string{"", "null", `{"version":99}`, `{"version":1,"secret":"do-not-echo"}`, strings.Repeat(" ", 4097)} {
		dir := t.TempDir()
		path := filepath.Join(dir, pendingName)
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := New(dir).Pending()
		if !errors.Is(err, identity.ErrInvalidProfile) || strings.Contains(err.Error(), "do-not-echo") {
			t.Fatal("invalid journal was accepted or echoed")
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != data {
			t.Fatal("journal changed")
		}
	}
}

func TestCommitNeverReplacesDifferentProfile(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	p := samplePending()
	old := p.Data.Profile
	old.Nickname = "another"
	if err := profilefile.New(dir).Create(old); err != nil {
		t.Fatal(err)
	}
	if err := store.Stage(p); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(p); !errors.Is(err, app.ErrProfileConflict) {
		t.Fatal(err)
	}
	got, err := profilefile.New(dir).Load()
	if err != nil || got != old {
		t.Fatal("existing profile replaced")
	}
	if _, err := store.Pending(); err != nil {
		t.Fatal("journal lost after conflict")
	}
}

func TestLockAndSpecialEntries(t *testing.T) {
	dir := t.TempDir()
	release, err := New(dir).Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir).Lock(); !errors.Is(err, app.ErrProfileBusy) {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := New(dir).Lock()
	if err != nil {
		t.Fatal(err)
	}
	if err := again(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"profile.json", "identity-seed.json", pendingName} {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := New(root).Load(); !errors.Is(err, identity.ErrInvalidProfile) {
			t.Fatal("special entry accepted")
		}
	}
}
