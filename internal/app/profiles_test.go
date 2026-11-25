package app_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profiledir"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profilefile"
	"github.com/bacrehclb/ShadowTalk/internal/storage/seedfile"
)

type memoryKeys struct {
	mu                        sync.Mutex
	entries                   map[string][]byte
	setErr, deleteErr, getErr error
}

func newMemoryKeys() *memoryKeys { return &memoryKeys{entries: make(map[string][]byte)} }
func (m *memoryKeys) Get(id string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	key, ok := m.entries[id]
	if !ok {
		return nil, identity.ErrEncryptionKeyNotFound
	}
	return bytes.Clone(key), nil
}
func (m *memoryKeys) Set(id string, key []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[id] = bytes.Clone(key) // Model an error after the OS already wrote.
	return m.setErr
}
func (m *memoryKeys) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.entries, id)
	return nil
}

func TestProfilesCreateReloadAndRefuseReplacement(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "-профиль с пробелами")
	keys := newMemoryKeys()
	first, err := app.NewProfiles(profiledir.New(dir), keys).Create("Ivan_01")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := app.NewProfiles(profiledir.New(dir), keys).Load()
	if err != nil || first.Profile != loaded.Profile || !bytes.Equal(first.Keys.PublicKey(), loaded.Keys.PublicKey()) {
		t.Fatalf("identity changed on reload: %v", err)
	}
	if len(first.Profile.Fingerprint()) != 71 {
		t.Fatal("missing full SHA-256 fingerprint")
	}
	if _, err := app.NewProfiles(profiledir.New(dir), keys).Create("another"); !errors.Is(err, identity.ErrProfileExists) {
		t.Fatal(err)
	}
	if len(keys.entries) != 1 {
		t.Fatal("duplicate creation wrote another key")
	}
	if _, err := profiledir.New(dir).Pending(); !errors.Is(err, app.ErrNoPendingProfile) {
		t.Fatal(err)
	}
	for _, name := range []string{"profile.json", "identity-seed.json"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range keys.entries {
			if bytes.Contains(data, key) {
				t.Fatal("plaintext key on disk")
			}
		}
	}
}

func TestProfilesUpgradePreservesLegacyMetadata(t *testing.T) {
	dir := t.TempDir()
	old := identity.Profile{Nickname: "Legacy_01", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	if err := profilefile.New(dir).Create(old); err != nil {
		t.Fatal(err)
	}
	keys := newMemoryKeys()
	profiles := app.NewProfiles(profiledir.New(dir), keys)
	before, err := profiles.Load()
	if err != nil || before.Profile.HasIdentity() || len(keys.entries) != 0 {
		t.Fatal("legacy read created identity")
	}
	upgraded, err := profiles.Upgrade()
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.Profile.Nickname != old.Nickname || !upgraded.Profile.CreatedAt.Equal(old.CreatedAt) || !upgraded.Profile.HasIdentity() {
		t.Fatal("upgrade lost metadata")
	}
	reloaded, err := profiles.Load()
	if err != nil || reloaded.Profile != upgraded.Profile {
		t.Fatal("upgrade did not persist")
	}
	if _, err := profiles.Upgrade(); !errors.Is(err, identity.ErrProfileExists) {
		t.Fatal(err)
	}
	if len(keys.entries) != 1 {
		t.Fatal("upgrade rotated an existing identity")
	}
}

func TestProfilesCleanupAndRecoverKeyWriteFailure(t *testing.T) {
	failure := errors.New("key store temporarily unavailable")
	for _, cleanupFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleanup succeeds", true: "recovery required"}[cleanupFails], func(t *testing.T) {
			dir := t.TempDir()
			files := profiledir.New(dir)
			keys := newMemoryKeys()
			keys.setErr = failure
			if cleanupFails {
				keys.deleteErr = failure
			}
			profiles := app.NewProfiles(files, keys)
			got, err := profiles.Create("test_01")
			if !errors.Is(err, failure) || got.Profile.HasIdentity() {
				t.Fatal("write failure was reported as success")
			}
			if !cleanupFails {
				if len(keys.entries) != 0 {
					t.Fatal("new key leaked after rollback")
				}
				if _, err := files.Pending(); !errors.Is(err, app.ErrNoPendingProfile) {
					t.Fatal(err)
				}
				keys.setErr = nil
				if _, err := profiles.Create("test_01"); err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, app.ErrProfilePending) {
				t.Fatal("missing recovery error")
			}
			pending, err := files.Pending()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := profiles.Create("other_01"); !errors.Is(err, app.ErrProfilePending) {
				t.Fatal(err)
			}
			keys.setErr, keys.deleteErr = nil, nil
			recovered, err := profiles.Recover()
			if err != nil || recovered.Profile != pending.Data.Profile {
				t.Fatalf("recovery changed identity: %v", err)
			}
			if len(keys.entries) != 1 {
				t.Fatal("recovery generated another key")
			}
		})
	}
}

// failCommit models a crash/failure before or after each file publication.
type failCommit struct {
	app.ProfileFiles
	directory string
	stage     int
}

func (f failCommit) Commit(p app.PendingProfile) error {
	switch f.stage {
	case 1:
		if err := seedfile.New(f.directory).Create(p.Data.KeyID, p.Data.EncryptedSeed); err != nil {
			return err
		}
	case 2:
		if err := f.ProfileFiles.Commit(p); err != nil {
			return err
		}
	}
	return errors.New("interrupted commit")
}

func TestProfilesRecoverEveryPublicationStage(t *testing.T) {
	for stage := 0; stage < 3; stage++ {
		t.Run(string(rune('0'+stage)), func(t *testing.T) {
			dir := t.TempDir()
			files := profiledir.New(dir)
			keys := newMemoryKeys()
			_, err := app.NewProfiles(failCommit{files, dir, stage}, keys).Create("test_01")
			if !errors.Is(err, app.ErrProfilePending) {
				t.Fatal(err)
			}
			pending, err := files.Pending()
			if err != nil {
				t.Fatal(err)
			}
			profiles := app.NewProfiles(files, keys)
			if _, err := profiles.Load(); !errors.Is(err, app.ErrProfilePending) {
				t.Fatal(err)
			}
			recovered, err := profiles.Recover()
			if err != nil || recovered.Profile != pending.Data.Profile {
				t.Fatalf("failed to resume exact identity: %v", err)
			}
			loaded, err := profiles.Load()
			if err != nil || !bytes.Equal(loaded.Keys.PublicKey(), recovered.Keys.PublicKey()) {
				t.Fatal("recovered identity cannot load")
			}
		})
	}
}

func TestProfilesMissingKeyRecoveryNeverReplacesPublishedIdentity(t *testing.T) {
	for _, stage := range []int{0, 1, 2} {
		t.Run(string(rune('0'+stage)), func(t *testing.T) {
			dir := t.TempDir()
			files := profiledir.New(dir)
			keys := newMemoryKeys()
			_, err := app.NewProfiles(failCommit{files, dir, stage}, keys).Create("test_01")
			if !errors.Is(err, app.ErrProfilePending) {
				t.Fatal(err)
			}
			clear(keys.entries) // Simulate losing the OS key, never a reason to regenerate.
			profiles := app.NewProfiles(files, keys)
			_, err = profiles.Recover()
			if stage == 0 {
				if !errors.Is(err, app.ErrCreationAborted) {
					t.Fatal(err)
				}
				if _, err := files.Pending(); !errors.Is(err, app.ErrNoPendingProfile) {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, app.ErrProfilePending) || !errors.Is(err, identity.ErrEncryptionKeyNotFound) {
					t.Fatal(err)
				}
				if _, err := files.Pending(); err != nil {
					t.Fatal("recovery evidence was removed")
				}
			}
			if len(keys.entries) != 0 {
				t.Fatal("new identity was generated after key loss")
			}
		})
	}
}

func TestProfilesRejectIdentitySubstitution(t *testing.T) {
	dir := t.TempDir()
	files := profiledir.New(dir)
	keys := newMemoryKeys()
	profiles := app.NewProfiles(files, keys)
	if _, err := profiles.Create("test_01"); err != nil {
		t.Fatal(err)
	}
	data, err := files.Load()
	if err != nil {
		t.Fatal(err)
	}
	_, replacement, ciphertext := encryptedIdentity(t)
	keys.entries[data.KeyID] = replacement
	if err := os.Remove(filepath.Join(dir, "identity-seed.json")); err != nil {
		t.Fatal(err)
	}
	if err := seedfile.New(dir).Create(data.KeyID, ciphertext); err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.Load(); !errors.Is(err, app.ErrIdentityMismatch) {
		t.Fatal(err)
	}
}

func TestProfilesConcurrentCreationHasOneWinner(t *testing.T) {
	dir := t.TempDir()
	keys := newMemoryKeys()
	start := make(chan struct{})
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			<-start
			_, err := app.NewProfiles(profiledir.New(dir), keys).Create("test_01")
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, app.ErrProfileBusy) && !errors.Is(err, identity.ErrProfileExists) {
			t.Fatal(err)
		}
	}
	if successes != 1 || len(keys.entries) != 1 {
		t.Fatal("concurrent creation produced multiple identities")
	}
	if _, err := app.NewProfiles(profiledir.New(dir), keys).Load(); err != nil {
		t.Fatal(err)
	}
}

func TestProfilesRecoverInterruptedLegacyUpgrade(t *testing.T) {
	for _, stage := range []int{0, 1, 2} {
		t.Run(string(rune('0'+stage)), func(t *testing.T) {
			dir := t.TempDir()
			files := profiledir.New(dir)
			keys := newMemoryKeys()
			old := identity.Profile{Nickname: "Legacy_01", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
			if err := profilefile.New(dir).Create(old); err != nil {
				t.Fatal(err)
			}
			_, err := app.NewProfiles(failCommit{files, dir, stage}, keys).Upgrade()
			if !errors.Is(err, app.ErrProfilePending) {
				t.Fatal(err)
			}
			pending, err := files.Pending()
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := app.NewProfiles(files, keys).Recover()
			if err != nil || recovered.Profile != pending.Data.Profile || recovered.Profile.Nickname != old.Nickname || !recovered.Profile.CreatedAt.Equal(old.CreatedAt) {
				t.Fatalf("upgrade recovery changed identity or metadata: %v", err)
			}
		})
	}
}
