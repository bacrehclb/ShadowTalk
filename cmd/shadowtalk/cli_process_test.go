package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profilefile"
	"github.com/charmbracelet/x/term"
)

// This file-only fake exists exclusively in the test binary. The production CLI
// has no flag or environment variable for storing plaintext keys in files.
type processTestKeys struct {
	directory           string
	crashAfterSet, deny bool
}

func (k processTestKeys) Get(id string) ([]byte, error) {
	if k.deny {
		return nil, errors.New("test key store unavailable")
	}
	key, err := os.ReadFile(filepath.Join(k.directory, id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, identity.ErrEncryptionKeyNotFound
	}
	return key, err
}
func (k processTestKeys) Set(id string, key []byte) error {
	if k.deny {
		return errors.New("test key store unavailable")
	}
	if err := os.WriteFile(filepath.Join(k.directory, id), key, 0o600); err != nil {
		return err
	}
	if k.crashAfterSet {
		os.Exit(90)
	} // No defers: verify OS lock release and recovery.
	return nil
}
func (k processTestKeys) Delete(id string) error {
	err := os.Remove(filepath.Join(k.directory, id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func TestCLIChildProcess(t *testing.T) {
	if os.Getenv("SHADOWTALK_TEST_CHILD") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		os.Exit(91)
	}
	keys := processTestKeys{
		directory:     os.Getenv("SHADOWTALK_TEST_KEYS"),
		crashAfterSet: os.Getenv("SHADOWTALK_TEST_CRASH") == "1",
		deny:          os.Getenv("SHADOWTALK_TEST_DENY") == "1",
	}
	if os.Getenv("SHADOWTALK_TEST_TERMINAL") == "1" {
		// Check before exiting: macOS may revoke the PTY slave when its session
		// leader exits, so the parent cannot reliably inspect termios afterward.
		terminal := os.Stdin
		if !term.IsTerminal(terminal.Fd()) {
			terminal = os.Stdout
		}
		before, err := term.GetState(terminal.Fd())
		if err != nil {
			os.Exit(92)
		}
		code := runWithKeys(args[1:], os.Stdout, os.Stderr, keys)
		after, err := term.GetState(terminal.Fd())
		if err != nil || !reflect.DeepEqual(before, after) {
			os.Exit(93)
		}
		os.Exit(code)
	}
	os.Exit(runWithKeys(args[1:], os.Stdout, os.Stderr, keys))
}

type processResult struct {
	code            int
	out, diagnostic string
}

func TestUIWithPipesFailsWithoutReadingOrCreatingProfile(t *testing.T) {
	work, keys := t.TempDir(), t.TempDir()
	profile := filepath.Join(work, "missing")
	base := []string{"--profile-dir", profile, "ui"}
	help := runCLIProcess(t, work, keys, nil, append(base, "--help")...)
	if help.code != 0 || help.diagnostic != "" || !strings.Contains(help.out, "shadowtalk") {
		t.Fatalf("ui help: %+v", help)
	}
	result := runCLIProcess(t, work, keys, nil, base...)
	if result.code != 1 || result.out != "" || !strings.Contains(result.diagnostic, "терминальные stdin и stdout") {
		t.Fatalf("ui pipe: %+v", result)
	}
	if _, err := os.Stat(profile); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("UI created profile without terminal")
	}
}

func runCLIProcess(t *testing.T, work, keys string, extraEnv []string, args ...string) processResult {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestCLIChildProcess$", "--"}, args...)...)
	command.Dir = work
	command.Env = append(os.Environ(), "SHADOWTALK_TEST_CHILD=1", "SHADOWTALK_TEST_KEYS="+keys)
	command.Env = append(command.Env, extraEnv...)
	var out, diagnostic bytes.Buffer
	command.Stdout, command.Stderr = &out, &diagnostic
	err = command.Run() // nil stdin gives the child EOF, not an interactive terminal.
	if ctx.Err() != nil {
		t.Fatal("CLI process timed out")
	}
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return processResult{code, out.String(), diagnostic.String()}
}

func TestCLIProcessesCreateRestartAndRecover(t *testing.T) {
	work, keyDir := t.TempDir(), t.TempDir()
	profileDir := filepath.Join(work, "-профиль с пробелами")
	call := func(crash bool, command ...string) processResult {
		env := []string{}
		if crash {
			env = append(env, "SHADOWTALK_TEST_CRASH=1")
		}
		return runCLIProcess(t, work, keyDir, env, append([]string{"--profile-dir", profileDir}, command...)...)
	}
	help := call(false, "--help")
	if help.code != 0 || help.diagnostic != "" || !strings.Contains(help.out, "profile recover") {
		t.Fatal(help)
	}
	if _, err := os.Stat(profileDir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("help created data")
	}
	crashed := call(true, "profile", "create", "--nick", "Ivan_01")
	if crashed.code != 90 || crashed.out != "" {
		t.Fatalf("expected injected process crash: %+v", crashed)
	}
	pending := call(false, "profile", "show")
	if pending.code == 0 || pending.out != "" || !strings.Contains(pending.diagnostic, "profile recover") {
		t.Fatal(pending)
	}
	recovered := call(false, "profile", "recover")
	if recovered.code != 0 || recovered.diagnostic != "" {
		t.Fatal(recovered)
	}
	first := call(false, "profile", "show")
	second := call(false, "profile", "show")
	if first.code != 0 || first.diagnostic != "" || !strings.Contains(first.out, "Отпечаток: SHA256:") || first != second {
		t.Fatal("identity changed across real CLI processes")
	}
	duplicate := call(false, "profile", "create", "--nick", "new_name")
	if duplicate.code == 0 || duplicate.out != "" || !strings.Contains(duplicate.diagnostic, "уже существует") {
		t.Fatal(duplicate)
	}
	if after := call(false, "profile", "show"); after != first {
		t.Fatal("duplicate creation changed profile")
	}
	entries, err := os.ReadDir(keyDir)
	if err != nil || len(entries) != 1 {
		t.Fatal("unexpected keys after process recovery")
	}
	// Loss of the key must fail, without replacing the identity or printing partial metadata.
	if err := os.Remove(filepath.Join(keyDir, entries[0].Name())); err != nil {
		t.Fatal(err)
	}
	missing := call(false, "profile", "show")
	if missing.code == 0 || missing.out != "" || !strings.Contains(missing.diagnostic, "отсутствует") {
		t.Fatal(missing)
	}
}

func TestCLIProcessesConcurrentCreation(t *testing.T) {
	work, keys := t.TempDir(), t.TempDir()
	dir := filepath.Join(work, "profile")
	start := make(chan struct{})
	results := make(chan processResult, 4)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			<-start
			results <- runCLIProcess(t, work, keys, nil, "--profile-dir", dir, "profile", "create", "--nick", "test_01")
		})
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for result := range results {
		if result.code == 0 {
			successes++
		} else if result.out != "" || (!strings.Contains(result.diagnostic, "другой операцией") && !strings.Contains(result.diagnostic, "уже существует")) {
			t.Fatal(result)
		}
	}
	if successes != 1 {
		t.Fatalf("got %d successful creators", successes)
	}
	entries, err := os.ReadDir(keys)
	if err != nil || len(entries) != 1 {
		t.Fatal("concurrent processes created extra keys")
	}
}

func TestCLIProcessesUpgradeLegacyAndFailWithoutKeyStore(t *testing.T) {
	work, keys := t.TempDir(), t.TempDir()
	dir := filepath.Join(work, "legacy")
	old := identity.Profile{Nickname: "Legacy_01", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	if err := profilefile.New(dir).Create(old); err != nil {
		t.Fatal(err)
	}
	legacy := runCLIProcess(t, work, keys, []string{"SHADOWTALK_TEST_DENY=1"}, "--profile-dir", dir, "profile", "show")
	if legacy.code != 0 || !strings.Contains(legacy.out, "profile upgrade") {
		t.Fatal(legacy)
	}
	failed := runCLIProcess(t, work, keys, []string{"SHADOWTALK_TEST_DENY=1"}, "--profile-dir", dir, "profile", "upgrade")
	if failed.code == 0 || failed.out != "" {
		t.Fatal(failed)
	}
	upgrade := runCLIProcess(t, work, keys, nil, "--profile-dir", dir, "profile", "upgrade")
	if upgrade.code != 0 || !strings.Contains(upgrade.out, "Отпечаток: SHA256:") {
		t.Fatal(upgrade)
	}
	loaded, err := profilefile.New(dir).Load()
	if err != nil || loaded.Nickname != old.Nickname || !loaded.CreatedAt.Equal(old.CreatedAt) || !loaded.HasIdentity() {
		t.Fatal("legacy upgrade lost profile metadata")
	}
}
