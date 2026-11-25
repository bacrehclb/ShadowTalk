package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/storage/keyring"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profiledir"
	"github.com/bacrehclb/ShadowTalk/internal/storage/seedfile"
)

// Explicit opt-in only: creates a disposable OS key and removes that same key.
func TestNativeKeyStoreProfileLifecycle(t *testing.T) {
	if os.Getenv("SHADOWTALK_NATIVE_KEYRING_TEST") != "1" {
		t.Skip("set SHADOWTALK_NATIVE_KEYRING_TEST=1 to test the real OS key store")
	}
	work := t.TempDir()
	dir := filepath.Join(work, "native-profile")
	binary := filepath.Join(work, "shadowtalk")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	t.Cleanup(func() {
		id, _, err := seedfile.New(dir).Load()
		if err != nil {
			if pending, pendingErr := profiledir.New(dir).Pending(); pendingErr == nil {
				id = pending.Data.KeyID
			}
		}
		if id != "" {
			if err := keyring.New().Delete(id); err != nil {
				t.Errorf("remove disposable OS key %s: %v", id, err)
			}
		}
	})
	call := func(args ...string) (string, string, error) {
		childCtx, childCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer childCancel()
		cmd := exec.CommandContext(childCtx, binary, append([]string{"--profile-dir", dir}, args...)...)
		cmd.Dir = work
		var out, diagnostic bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &diagnostic
		err := cmd.Run()
		return out.String(), diagnostic.String(), err
	}
	out, diagnostic, err := call("profile", "create", "--nick", "native_test")
	if err != nil || diagnostic != "" || out != "Профиль создан: native_test\n" {
		t.Fatalf("native create: %v, diagnostic: %s", err, diagnostic)
	}
	first, diagnostic, err := call("profile", "show")
	if err != nil || diagnostic != "" || !strings.Contains(first, "Отпечаток: SHA256:") {
		t.Fatalf("native show: %v, diagnostic: %s", err, diagnostic)
	}
	second, diagnostic, err := call("profile", "show")
	if err != nil || diagnostic != "" || first != second {
		t.Fatal("native identity changed across CLI restarts")
	}
}
