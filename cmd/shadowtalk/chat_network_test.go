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
