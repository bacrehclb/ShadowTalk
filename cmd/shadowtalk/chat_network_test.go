package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/storage/torconfig"
	"github.com/bacrehclb/ShadowTalk/internal/torbundle"
	"github.com/bacrehclb/ShadowTalk/internal/transport/tor"
)

func TestChatNetworkConfigurationPrecedenceAndFailure(t *testing.T) {
	dir := t.TempDir()
	f := chatNetworkFlags{binary: "flag-tor", timeout: time.Minute}
	o, err := f.options(dir)
	if err != nil || o.BinaryPath != "tor" || o.StartTimeout != 3*time.Minute {
		t.Fatal(o, err)
	}
	path := filepath.Join(dir, torconfig.FileName)
	if err := os.WriteFile(path, []byte(`{"version":1,"mode":"direct","tor_binary":"saved-tor","startup_timeout":"5m"}`), 0600); err != nil {
		t.Fatal(err)
	}
	o, err = f.options(dir)
	if err != nil || o.BinaryPath != filepath.Join(dir, "saved-tor") || o.StartTimeout != 5*time.Minute {
		t.Fatal(o, err)
	}
	f.binarySet, f.timeoutSet = true, true
	o, err = f.options(dir)
	if err != nil || o.BinaryPath != f.binary || o.StartTimeout != f.timeout {
		t.Fatal(o, err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"mode":"obfs4","bridges":["PRIVATE"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.options(dir); !errors.Is(err, torconfig.ErrInvalid) {
		t.Fatal("flags bypassed invalid saved config", err)
	}
	f.config = filepath.Join(dir, "missing")
	if _, err := f.options(dir); !errors.Is(err, torconfig.ErrRead) {
		t.Fatal("explicit missing config fell back", err)
	}
}

func TestBundledTorSelectionAndExplicitOverrides(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "shadowtalk")
	base := filepath.Join(dir, "tor-runtime", "linux-amd64", "tor")
	for _, path := range []string{executable, filepath.Join(base, "tor"), filepath.Join(base, "pluggable_transports", "lyrebird")} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	base, _ = filepath.EvalSymlinks(base)
	f := chatNetworkFlags{}
	o, err := f.optionsFor(dir, executable, "linux", "amd64")
	if err != nil || o.BinaryPath != filepath.Join(base, "tor") || o.LibraryDir != base {
		t.Fatal(o, err)
	}
	f.binarySet, f.binary = true, "tor"
	o, err = f.optionsFor(dir, executable, "linux", "amd64")
	if err != nil || o.BinaryPath != "tor" || o.LibraryDir != "" {
		t.Fatal("explicit PATH override ignored", o, err)
	}
	f = chatNetworkFlags{}
	bridge := "obfs4 192.0.2.1:443 " + strings.Repeat("A", 40) + " cert=" + strings.Repeat("A", 70) + " iat-mode=0"
	config := `{"version":1,"mode":"obfs4","bridges":["` + bridge + `"]}`
	path := filepath.Join(dir, torconfig.FileName)
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	o, err = f.optionsFor(dir, executable, "linux", "amd64")
	if err != nil || o.Connection.LyrebirdBinary != filepath.Join(base, "pluggable_transports", "lyrebird") {
		t.Fatal(o, err)
	}
	if err := os.Remove(filepath.Join(base, "tor")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.optionsFor(dir, executable, "linux", "amd64"); !errors.Is(err, torbundle.ErrIncomplete) {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"mode":"direct","tor_binary":"custom-tor"}`), 0600); err != nil {
		t.Fatal(err)
	}
	o, err = f.optionsFor(dir, executable, "linux", "amd64")
	if err != nil || o.BinaryPath != filepath.Join(dir, "custom-tor") || o.LibraryDir != "" {
		t.Fatal("JSON override ignored", o, err)
	}
}

func TestConnectionDiagnosticsRetainSafeStageNotRawCause(t *testing.T) {
	err := &tor.BootstrapError{Progress: tor.Progress{Phase: tor.Bootstrapping, Percent: 14, Step: tor.BootstrapHandshake, Problem: tor.BootstrapTimeout}, Cause: errors.Join(context.DeadlineExceeded, errors.New("PRIVATE bridge cert"))}
	message := chatUserError(err)
	if !strings.Contains(message, "14%") || !strings.Contains(message, "рукопожатие") || !strings.Contains(message, "tor.json") || strings.Contains(message, "PRIVATE") {
		t.Fatal(message)
	}
	for _, err := range []error{torconfig.ErrInvalid, torconfig.ErrRead, torconfig.ErrUnsafeFile, tor.ErrObfs4Startup, tor.ErrObfs4Exited} {
		if message := chatUserError(errors.Join(err, errors.New("PRIVATE"))); strings.Contains(message, "PRIVATE") || message == "" {
			t.Fatal(message)
		}
	}
}
