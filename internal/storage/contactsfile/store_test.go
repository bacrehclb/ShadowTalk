package contactsfile_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/storage/contactsfile"
	"github.com/gofrs/flock"
)

var now = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

func keys(t testing.TB) identity.KeyPair {
	t.Helper()
	k, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func invitation(t testing.TB, k identity.KeyPair) (string, contacts.Invitation) {
	t.Helper()
	token, err := contacts.CreateInvitation(k, "alice", now, contacts.DefaultLifetime)
	if err != nil {
		t.Fatal(err)
	}
	i, err := contacts.ParseInvitation(token, now)
	if err != nil {
		t.Fatal(err)
	}
	return token, i
}

func TestPersistentConfirmationLifecycleAndOwnerBinding(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "контакты с пробелами")
	owner := [32]byte(keys(t).PublicKey())
	store := contactsfile.New(directory, owner)
	service := app.NewContacts(store)
	if entries, err := service.List(); err != nil || len(entries) != 0 {
		t.Fatalf("empty read: %v", err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created directory")
	}
	token, invite := invitation(t, keys(t))
	if _, err := service.Import(token, "alice", "", now); !errors.Is(err, contacts.ErrConfirmationRequired) {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unconfirmed import created directory")
	}
	saved, err := service.Import(token, "alice", invite.Fingerprint(), now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "contacts.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(before, []byte(token)) || bytes.Contains(before, []byte("signature")) {
		t.Fatal("store retained full invitation")
	}
	restarted := app.NewContacts(contactsfile.New(directory, owner))
	got, err := restarted.Get("alice")
	if err != nil || got != saved {
		t.Fatalf("restart: %+v, %v", got, err)
	}
	if _, err := restarted.Import(token, "alice", invite.Fingerprint(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("reimport rewrote contact timestamps")
	}
	otherToken, other := invitation(t, keys(t))
	if _, err := restarted.Import(otherToken, "alice", other.Fingerprint(), now); !errors.Is(err, contacts.ErrKeyChanged) {
		t.Fatalf("replacement: %v", err)
	}
	if err := restarted.Remove("alice", other.Fingerprint()); !errors.Is(err, contacts.ErrFingerprintMismatch) {
		t.Fatal(err)
	}
	after, _ = os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected change modified contacts")
	}
	wrongOwner := contactsfile.New(directory, [32]byte(keys(t).PublicKey()))
	if _, err := wrongOwner.Read(); !errors.Is(err, contactsfile.ErrOwnerMismatch) {
		t.Fatal(err)
	}
	if err := wrongOwner.Update(func(*contacts.Book) error { t.Error("callback ran for wrong owner"); return nil }); !errors.Is(err, contactsfile.ErrOwnerMismatch) {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Fatalf("contact permissions: %s", info.Mode())
		}
	}
	if err := restarted.Remove("alice", invite.Fingerprint()); err != nil {
		t.Fatal(err)
	}
	if entries, err := service.List(); err != nil || len(entries) != 0 {
		t.Fatalf("remove: %v", err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(directory, ".contacts-*.tmp"))
	if len(leftovers) != 0 {
		t.Fatal("temporary files left behind")
	}
}

func TestCorruptContactsArePreservedAndNotReset(t *testing.T) {
	for _, kind := range []string{"broken", "duplicate-field", "unknown-field", "large", "new-version", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			owner := [32]byte(keys(t).PublicKey())
			store := contactsfile.New(directory, owner)
			token, invite := invitation(t, keys(t))
			service := app.NewContacts(store)
			if _, err := service.Import(token, "alice", invite.Fingerprint(), now); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "contacts.json")
			data, _ := os.ReadFile(path)
			want := contactsfile.ErrInvalidRecord
			switch kind {
			case "broken":
				data = []byte("{broken")
			case "duplicate-field":
				data = []byte(strings.Replace(string(data), `"version": 1`, `"version": 1,"version": 1`, 1))
			case "unknown-field":
				data = []byte(strings.Replace(string(data), `"version": 1`, `"version": 1,"unknown": 0`, 1))
			case "large":
				data = bytes.Repeat([]byte(" "), (1<<20)+1)
			case "new-version":
				data = []byte(strings.Replace(string(data), `"version": 1`, `"version": 2`, 1))
				want = contactsfile.ErrUnsupportedVersion
			case "symlink":
				target := filepath.Join(t.TempDir(), "original.json")
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			if kind != "symlink" {
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.Read(); !errors.Is(err, want) {
				t.Fatalf("read: %v", err)
			}
			if err := store.Update(func(*contacts.Book) error { t.Error("callback ran on corrupt data"); return nil }); !errors.Is(err, want) {
				t.Fatalf("update: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, after) {
				t.Fatal("invalid contacts replaced")
			}
		})
	}
}

func TestFailedChangeAndBusyWriterPreserveSnapshot(t *testing.T) {
	directory := t.TempDir()
	owner := [32]byte(keys(t).PublicKey())
	store := contactsfile.New(directory, owner)
	token, invite := invitation(t, keys(t))
	contact, _ := contacts.ConfirmInvitation(token, "alice", invite.Fingerprint(), now)
	failure := errors.New("application rejected change")
	err := store.Update(func(book *contacts.Book) error { _, _ = book.Add(contact); return failure })
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "contacts.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed callback published contact")
	}
	otherOwner := [32]byte(keys(t).PublicKey())
	err = store.Update(func(book *contacts.Book) error {
		other, _ := contacts.RestoreBook(otherOwner, nil)
		*book = *other
		return nil
	})
	if !errors.Is(err, contactsfile.ErrOwnerMismatch) {
		t.Fatalf("callback changed owner: %v", err)
	}
	lock := flock.New(filepath.Join(directory, ".contacts.lock"))
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := store.Update(func(*contacts.Book) error { t.Error("callback ran while busy"); return nil }); !errors.Is(err, contactsfile.ErrBusy) {
		t.Fatal(err)
	}
}

func TestConcurrentWritersDoNotLoseConfirmedContacts(t *testing.T) {
	directory := t.TempDir()
	owner := [32]byte(keys(t).PublicKey())
	const count = 12
	entries := make([]contacts.Contact, count)
	for i := range entries {
		token, invite := invitation(t, keys(t))
		entries[i], _ = contacts.ConfirmInvitation(token, fmt.Sprintf("peer%02d", i), invite.Fingerprint(), now)
	}
	var writers sync.WaitGroup
	for _, contact := range entries {
		writers.Go(func() {
			deadline := time.Now().Add(5 * time.Second)
			for {
				err := contactsfile.New(directory, owner).Update(func(book *contacts.Book) error { _, err := book.Add(contact); return err })
				if !errors.Is(err, contactsfile.ErrBusy) || time.Now().After(deadline) {
					if err != nil {
						t.Error(err)
					}
					return
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
	writers.Wait()
	book, err := contactsfile.New(directory, owner).Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(book.List()) != count {
		t.Fatalf("lost writes: %d contacts", len(book.List()))
	}
}
