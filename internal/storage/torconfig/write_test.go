package torconfig

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWritePreservesDiscoveryPermissionsAndDetectsStaleEditor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "профиль с пробелами", FileName)
	s := Defaults()
	if err := Write(path, nil, s); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "tor_binary") {
		t.Fatal("automatic discovery became an explicit path")
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	old, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	next := old
	next.StartTimeout = 5 * time.Minute
	if err := Write(path, &old, next); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, &old, s); !errors.Is(err, ErrChanged) {
		t.Fatal("stale editor overwrote changes", err)
	}
	current, _ := Read(path)
	if current.StartTimeout != 5*time.Minute {
		t.Fatal("new settings were lost")
	}
	if err := Write(path, nil, s); !errors.Is(err, ErrChanged) {
		t.Fatal(err)
	}
}

func TestWriteInvalidOrMalformedConfigLeavesOriginalUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := Write(path, nil, Defaults()); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)
	previous, _ := Read(path)
	bad := previous
	bad.StartTimeout = 16 * time.Minute
	if err := Write(path, &previous, bad); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != string(original) {
		t.Fatal("invalid input changed config")
	}
	corrupt := []byte(`{"version":99,"mode":"direct"}`)
	if err := os.WriteFile(path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, &previous, Defaults()); !errors.Is(err, ErrVersion) {
		t.Fatal(err)
	}
	unchanged, _ = os.ReadFile(path)
	if string(unchanged) != string(corrupt) {
		t.Fatal("unsupported config overwritten")
	}
}

func TestWriteRefusesSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "keep")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, FileName)
	if err := os.Symlink(target, path); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks unavailable")
		}
		t.Fatal(err)
	}
	if err := Write(path, nil, Defaults()); !errors.Is(err, ErrUnsafeFile) {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "keep" {
		t.Fatal("symlink target changed")
	}
}
