package profilefile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

func exampleProfile(nickname string) identity.Profile {
	return identity.Profile{
		Nickname:  nickname,
		CreatedAt: time.Date(2026, 11, 21, 12, 0, 0, 123456789, time.UTC),
	}
}

func TestCreateAndLoadAcrossStoreInstances(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "profile")
	want := exampleProfile("Ivan_01")
	if err := New(directory).Create(want); err != nil {
		t.Fatal(err)
	}
	got, err := New(directory).Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	assertOnlyProfileFile(t, directory)
	if runtime.GOOS != "windows" {
		for path, wantMode := range map[string]fs.FileMode{
			directory:                          0o700,
			filepath.Join(directory, fileName): 0o600,
		} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != wantMode {
				t.Errorf("%s: permissions %o, want %o", path, info.Mode().Perm(), wantMode)
			}
		}
	}
}

func TestLoadMissingProfileDoesNotCreateDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "missing")
	if _, err := New(directory).Load(); !errors.Is(err, identity.ErrProfileNotFound) {
		t.Fatalf("got %v, want ErrProfileNotFound", err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("load created a directory: %v", err)
	}
}

func TestCreateRejectsInvalidProfileBeforeWriting(t *testing.T) {
	for _, profile := range []identity.Profile{
		exampleProfile("bad.name"),
		{Nickname: "ivan"},
	} {
		directory := filepath.Join(t.TempDir(), "missing")
		if err := New(directory).Create(profile); err == nil {
			t.Fatal("invalid profile accepted")
		}
		if _, err := os.Stat(directory); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("invalid profile created a directory: %v", err)
		}
	}
}

func TestCreateDoesNotReplaceExistingProfile(t *testing.T) {
	directory := t.TempDir()
	store := New(directory)
	original := exampleProfile("original")
	if err := store.Create(original); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(exampleProfile("replacement")); !errors.Is(err, identity.ErrProfileExists) {
		t.Fatalf("got %v, want ErrProfileExists", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != original {
		t.Fatalf("existing profile changed: %+v", got)
	}
	assertOnlyProfileFile(t, directory)
}

func TestUnreadableRecordsArePreserved(t *testing.T) {
	tests := []struct {
		name string
		data string
		want error
	}{
		{"empty", "", identity.ErrInvalidProfile},
		{"truncated", `{"version":1,`, identity.ErrInvalidProfile},
		{"null", `null`, identity.ErrInvalidProfile},
		{"missing version", `{"nickname":"ivan","created_at":"2026-11-21T12:00:00Z"}`, identity.ErrInvalidProfile},
		{"unknown version", `{"version":2,"nickname":"ivan","created_at":"2026-11-21T12:00:00Z"}`, ErrUnsupportedVersion},
		{"invalid nickname", `{"version":1,"nickname":"bad.name","created_at":"2026-11-21T12:00:00Z"}`, identity.ErrInvalidProfile},
		{"missing time", `{"version":1,"nickname":"ivan"}`, identity.ErrInvalidProfile},
		{"malformed time", `{"version":1,"nickname":"ivan","created_at":"yesterday"}`, identity.ErrInvalidProfile},
		{"trailing record", `{"version":1,"nickname":"ivan","created_at":"2026-11-21T12:00:00Z"} {}`, identity.ErrInvalidProfile},
		{"trailing junk", `{"version":1,"nickname":"ivan","created_at":"2026-11-21T12:00:00Z"} junk`, identity.ErrInvalidProfile},
		{"unknown field", `{"version":1,"nickname":"ivan","created_at":"2026-11-21T12:00:00Z","extra":true}`, identity.ErrInvalidProfile},
		{"oversized", strings.Repeat(" ", maxProfileSize+1), identity.ErrInvalidProfile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, fileName)
			if err := os.WriteFile(path, []byte(tt.data), 0o600); err != nil {
				t.Fatal(err)
			}
			store := New(directory)
			if _, err := store.Load(); !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			if err := store.Create(exampleProfile("ivan")); !errors.Is(err, identity.ErrProfileExists) {
				t.Fatalf("create over invalid record: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.data {
				t.Fatal("existing record changed")
			}
			assertOnlyProfileFile(t, directory)
		})
	}
}

func TestConcurrentCreatePublishesExactlyOneCompleteProfile(t *testing.T) {
	directory := t.TempDir()
	store := New(directory)
	const count = 16
	type result struct {
		profile identity.Profile
		err     error
	}
	results := make(chan result, count)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		workers.Go(func() {
			<-start
			profile := exampleProfile(fmt.Sprintf("user_%02d", i))
			results <- result{profile, store.Create(profile)}
		})
	}
	close(start)
	workers.Wait()
	close(results)

	var winner identity.Profile
	successes := 0
	for result := range results {
		if result.err == nil {
			successes++
			winner = result.profile
		} else if !errors.Is(result.err, identity.ErrProfileExists) {
			t.Errorf("unexpected create error: %v", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("got %d successful creators, want 1", successes)
	}
	got, err := New(directory).Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != winner {
		t.Fatalf("stored profile %+v differs from winning profile %+v", got, winner)
	}
	assertOnlyProfileFile(t, directory)
}

func TestCreateDoesNotReplaceExistingDirectory(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, fileName)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := New(directory).Create(exampleProfile("ivan")); !errors.Is(err, identity.ErrProfileExists) {
		t.Fatalf("got %v, want ErrProfileExists", err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Fatalf("existing directory changed: %v", err)
	}
}

func assertOnlyProfileFile(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != fileName {
		t.Fatalf("unexpected directory contents: %v", entries)
	}
}
