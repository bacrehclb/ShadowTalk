package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bacrehclb/ShadowTalk/internal/history"
)

func TestChatCLIHelpAndInvalidInputHaveNoSideEffects(t *testing.T) {
	work, keys := t.TempDir(), t.TempDir()
	profile := filepath.Join(work, "missing profile")
	base := []string{"--profile-dir", profile, "chat"}
	help := runCLIProcess(t, work, keys, []string{"SHADOWTALK_TEST_DENY=1"}, append(base, "--help")...)
	if help.code != 0 || help.diagnostic != "" || !strings.Contains(help.out, "--tor-config") || !strings.Contains(help.out, "Ctrl+C") {
		t.Fatalf("help: %+v", help)
	}
	for _, args := range [][]string{nil, {"--unknown"}, {"--contact"}, {"--contact", "bad alias"}, {"--startup-timeout", "0s"}, {"--startup-timeout", "16m"}, {"--tor-binary", ""}, {"unexpected"}} {
		result := runCLIProcess(t, work, keys, nil, append(base, args...)...)
		if result.code != 1 || result.out != "" || result.diagnostic == "" {
			t.Fatalf("args=%v result=%+v", args, result)
		}
		if args == nil && !strings.Contains(result.diagnostic, "терминальные stdin и stdout") {
			t.Fatal(result.diagnostic)
		}
	}
	if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("help or invalid terminal created data")
	}
}

func TestChatErrorsDoNotEchoUntrustedPayloads(t *testing.T) {
	for _, err := range []error{errors.New("\x1b]52;c;private-text\a"), errors.Join(history.ErrStorage, errors.New("private-text"))} {
		if text := chatUserError(err); strings.Contains(text, "private-text") || strings.ContainsAny(text, "\x1b\a") {
			t.Fatal("unsafe diagnostic")
		}
	}
}
