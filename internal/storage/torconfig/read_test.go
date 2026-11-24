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

func TestReadSavedSettingsAndRelativePaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(`{"version":1,"mode":"direct","tor_binary":"bin/путь с пробелами/tor","startup_timeout":"5m"}`), 0600); err != nil {
		t.Fatal(err)
	}
	settings, err := Read(path)
	if err != nil || settings.BinaryPath != filepath.Join(dir, "bin", "путь с пробелами", "tor") || settings.StartTimeout != 5*time.Minute {
		t.Fatal(settings, err)
	}
	if _, err := Read(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestInvalidConfigNeverFallsBackOrLeaksInput(t *testing.T) {
	for _, text := range []string{
		`{}`, `null`, `{"version":1,"mode":"direct"} {}`, `{"version":1,"mode":"direct","mode":"obfs4"}`,
		`{"version":1,"Mode":"direct"}`, `{"version":1,"mode":"direct","UseBridges":0}`,
		`{"version":1,"mode":"direct","tor_binary":null}`, `{"version":1,"mode":"direct","tor_binary":""}`,
		`{"version":1,"mode":"direct","startup_timeout":"0s"}`, `{"version":1,"mode":"direct","startup_timeout":"16m"}`,
		`{"version":1,"mode":"direct","bridges":["PRIVATE"]}`, `{"version":1,"mode":"obfs4","lyrebird_binary":"pt","bridges":["PRIVATE"]}`,
		`{"version":1,"mode":"direct","tor_binary":"PRIVATE\npath"}`, `{"version":1,"mode":"direct","bridges":null}`,
	} {
		_, err := decode([]byte(text), t.TempDir())
		if !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatal("bad config accepted or disclosed", err)
		}
	}
	if _, err := decode([]byte(`{"version":2,"mode":"direct"}`), t.TempDir()); !errors.Is(err, ErrVersion) {
		t.Fatal(err)
	}
}

func TestConfigFileLimitsPermissionsAndLinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", maxSize+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("oversized file accepted")
	}
	if _, err := Read(dir); !errors.Is(err, ErrUnsafeFile) {
		t.Fatal("directory accepted", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"mode":"direct"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); !errors.Is(err, ErrUnsafeFile) {
		t.Fatal("shared file accepted", err)
	}
	link := filepath.Join(dir, "broken-link")
	if err := os.Symlink(filepath.Join(dir, "missing"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(link); !errors.Is(err, ErrUnsafeFile) {
		t.Fatal("broken symlink treated as absent", err)
	}
}
