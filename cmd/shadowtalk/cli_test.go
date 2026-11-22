package main

import (
	"bytes"
	"errors"
	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func invoke(args ...string) (int, string, string) {
	return invokeWithKeys(make(memoryKeys), args...)
}

func invokeWithKeys(keys app.EncryptionKeys, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := runWithKeys(args, &stdout, &stderr, keys)
	return code, stdout.String(), stderr.String()
}

func TestProfileLifecycle(t *testing.T) {
	keys := make(memoryKeys)
	invoke := func(args ...string) (int, string, string) { return invokeWithKeys(keys, args...) }
	directory := filepath.Join(t.TempDir(), "profile with spaces")
	base := []string{"--profile-dir", directory, "profile"}
	code, stdout, stderr := invoke(append(base, "create", "--nick", "Ivan_01")...)
	if code != 0 || stdout != "Профиль создан: Ivan_01\n" || stderr != "" {
		t.Fatalf("create: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, original, stderr := invoke(append(base, "show")...)
	if code != 0 || !strings.Contains(original, "Ник: Ivan_01\nСоздан: ") || stderr != "" {
		t.Fatalf("show: code=%d stdout=%q stderr=%q", code, original, stderr)
	}
	code, stdout, stderr = invoke(append(base, "create", "--nick", "replacement")...)
	if code == 0 || stdout != "" || !strings.Contains(stderr, "уже существует") {
		t.Fatalf("duplicate create: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = invoke(append(base, "show")...)
	if code != 0 || stdout != original || stderr != "" {
		t.Fatalf("profile changed after duplicate create: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRejectedCommandsDoNotCreateProfile(t *testing.T) {
	for _, args := range [][]string{
		{"profile", "create"},
		{"profile", "create", "--nick", "bad.name"},
		{"profile", "create", "--nick", "ivan", "extra"},
		{"profile", "create", "--unknown", "ivan"},
		{"profile", "show"},
		{"profile", "show", "extra"},
		{"profile", "show", "--nick", "ivan"},
		{"profile", "delete"},
		{"unknown"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "missing")
			code, stdout, stderr := invoke(append([]string{"--profile-dir", directory}, args...)...)
			if code == 0 || stdout != "" || stderr == "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if _, err := os.Stat(directory); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("rejected command changed filesystem: %v", err)
			}
		})
	}
}

func TestHelpDoesNotAccessProfile(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--help"},
		{"profile"},
		{"profile", "--help"},
		{"profile", "create", "--help"},
		{"profile", "show", "--help"},
		{"profile", "upgrade", "--help"},
		{"profile", "recover", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "missing")
			code, stdout, stderr := invoke(append([]string{"--profile-dir", directory}, args...)...)
			if code != 0 || !strings.Contains(stdout, "Использование:") || stderr != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if _, err := os.Stat(directory); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("help changed filesystem: %v", err)
			}
		})
	}
}

type memoryKeys map[string][]byte

func (m memoryKeys) Get(id string) ([]byte, error) {
	key, ok := m[id]
	if !ok {
		return nil, identity.ErrEncryptionKeyNotFound
	}
	return bytes.Clone(key), nil
}
func (m memoryKeys) Set(id string, key []byte) error { m[id] = bytes.Clone(key); return nil }
func (m memoryKeys) Delete(id string) error          { delete(m, id); return nil }

func TestCorruptProfileIsReportedAndPreserved(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "profile.json")
	data := []byte(`{"version":1,`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := invoke("--profile-dir", directory, "profile", "show")
	if code == 0 || stdout != "" || !strings.Contains(stderr, "повреждён") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("corrupt profile changed: data=%q err=%v", got, err)
	}
}
