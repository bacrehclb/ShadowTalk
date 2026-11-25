package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionProcessDoesNotAccessProfile(t *testing.T) {
	work, keys := t.TempDir(), t.TempDir()
	profile := filepath.Join(work, "профиль with spaces")
	result := runCLIProcess(t, work, keys, []string{"SHADOWTALK_TEST_DENY=1"},
		"--profile-dir", profile, "--version")
	if result.code != 0 || result.diagnostic != "" || !strings.HasPrefix(result.out, "shadowtalk dev (commit unknown,") {
		t.Fatalf("version: %+v", result)
	}
	if _, err := os.Stat(profile); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("version touched profile: %v", err)
	}
	for _, args := range [][]string{
		{"--version", "profile", "create", "--nick", "alice"},
		{"--version", "extra"},
		{"--version", "--unknown"},
	} {
		result := runCLIProcess(t, work, keys, nil, args...)
		if result.code == 0 || result.out != "" || result.diagnostic == "" {
			t.Fatalf("invalid version arguments %q: %+v", args, result)
		}
	}
}
