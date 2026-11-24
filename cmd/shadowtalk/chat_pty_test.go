//go:build darwin || linux

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/delivery"
	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
	"github.com/bacrehclb/ShadowTalk/internal/storage/contactsfile"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profiledir"
	"github.com/bacrehclb/ShadowTalk/internal/storage/sqlite"
	"github.com/bacrehclb/ShadowTalk/internal/storage/torconfig"
	"github.com/creack/pty"
)

func chatTorHelper(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tor-helper")
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-c", "-o", binary, "./internal/transport/tor")
	cmd.Dir = filepath.Join("..", "..")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build Tor fixture: %v\n%s", err, output)
	}
	return binary
}

func chatProfile(t *testing.T, dir, keys, nick string) app.LoadedProfile {
	t.Helper()
	p, err := app.NewProfiles(profiledir.New(dir), processTestKeys{directory: keys}).Create(nick)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func chatContact(t *testing.T, dir string, local, remote app.LoadedProfile) {
	t.Helper()
	address, err := remote.Keys.OnionAddress()
	if err != nil {
		t.Fatal(err)
	}
	if err := contactsfile.New(dir, local.Profile.PublicKey).Update(func(book *contacts.Book) error {
		_, err := book.Add(contacts.Contact{Alias: remote.Profile.Nickname, Nickname: remote.Profile.Nickname, PublicKey: remote.Profile.PublicKey, Onion: address, AddedAt: time.Now()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
func chatStore(t *testing.T, dir string, p app.LoadedProfile) *sqlite.Store {
	t.Helper()
	key, err := p.Keys.HistoryKey()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	s, err := sqlite.Open(t.Context(), dir, p.Profile.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestChatInTerminalExchangesPersistsAndRestores(t *testing.T) {
	helper := chatTorHelper(t)
	registry := t.TempDir()
	env := []string{"SHADOWTALK_FAKE_TOR=normal", "SHADOWTALK_FAKE_REGISTRY=" + registry}
	aDir, bDir := filepath.Join(t.TempDir(), "профиль alice"), filepath.Join(t.TempDir(), "bob")
	aKeys, bKeys := t.TempDir(), t.TempDir()
	alice, bob := chatProfile(t, aDir, aKeys, "alice"), chatProfile(t, bDir, bKeys, "bob")
	chatContact(t, aDir, alice, bob)
	chatContact(t, bDir, bob, alice)
	store := chatStore(t, aDir, alice)
	_, _, err := store.Append(t.Context(), history.Message{Peer: bob.Profile.PublicKey, Direction: history.Incoming, ID: protocol.MessageID{1}, Text: "stored marker\n\x1b]52;c;unsafe\a\x1b[2J\u202e", RecordedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	args := []string{"chat", "--tor-binary", helper, "--startup-timeout", "10s"}
	a := startTerminalArgs(t, aDir, aKeys, false, false, false, env, append(args, "--contact", "bob")...)
	b := startTerminalArgs(t, bDir, bKeys, false, false, false, env, append(args, "--contact", "alice")...)
	a.expect(0, "Tor готов")
	b.expect(0, "Tor готов")
	a.expect(0, "stored marker")
	if strings.Contains(a.snapshot(), "\x1b]52;c;unsafe") || strings.Contains(a.snapshot(), "\u202e") {
		t.Fatal("stored text executed terminal controls")
	}
	mark := a.send("\x1b[200~hello terminal\x1b[201~")
	a.expect(mark, "hello terminal")
	if err := pty.Setsize(a.master, &pty.Winsize{Rows: 10, Cols: 30}); err != nil {
		t.Fatal(err)
	}
	a.expect(mark, "Увеличьте окно")
	mark = len(a.snapshot())
	if err := pty.Setsize(a.master, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}
	a.expect(mark, "hello terminal")
	mark = a.send("\r")
	b.expect(0, "hello terminal")
	a.expect(mark, "доставлено")
	mark = b.send("\x1b[200~reply from bob\x1b[201~\r")
	b.expect(mark, "доставлено")
	a.expect(0, "reply from bob")
	a.send("\x03")
	a.finish(true)
	b.send("\x1b")
	b.finish(true)
	assertNoColorSGR(t, a.snapshot())
	assertNoColorSGR(t, b.snapshot())
	store = chatStore(t, aDir, alice)
	page, err := store.List(t.Context(), bob.Profile.PublicKey, history.Query{})
	if err != nil || len(page.Entries) != 3 {
		t.Fatal("terminal messages did not persist", err)
	}
	var outgoing history.Entry
	for _, entry := range page.Entries {
		if entry.Direction == history.Outgoing {
			outgoing = entry
		}
	}
	item, err := store.Status(t.Context(), bob.Profile.PublicKey, outgoing.ID)
	if err != nil || item.State != delivery.Delivered {
		t.Fatal("terminal delivery status", err)
	}
	store.Close()
	restarted := startTerminalArgs(t, aDir, aKeys, false, false, false, env, args...)
	restarted.expect(0, "reply from bob")
	restarted.send("\x03")
	restarted.finish(true)

	// Reuse the same fixture binary to test bounded cancellation during startup.
	env[0] = "SHADOWTALK_FAKE_TOR=stall-port"
	stalled := startTerminalArgs(t, aDir, aKeys, false, false, false, env, args...)
	stalled.expect(0, "Подключение Tor")
	stalled.send("\x03")
	stalled.finish(true)
	paths, err := filepath.Glob(filepath.Join(aDir, "tor", ".shadowtalk-run-*"))
	if err != nil || len(paths) != 0 {
		t.Fatal("Tor runtime files leaked", err)
	}
	chatStore(t, aDir, alice).Close() // Cancellation released the history lock too.
}

func TestChatRejectsUnsupportedTerminalsBeforeOpeningProfile(t *testing.T) {
	for _, mode := range []string{"stdin-pipe", "stdout-pipe", "dumb"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "missing")
			p := startTerminalArgs(t, path, t.TempDir(), mode == "stdin-pipe", mode == "stdout-pipe", mode == "dumb", nil, "chat")
			p.finish(false)
			if p.snapshot() != "" || p.pipedOutput.Len() != 0 {
				t.Fatal("unsupported terminal rendered chat")
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unsupported terminal created profile")
			}
		})
	}
}

func TestChatLoadsConnectionConfigShowsProgressAndReloadsAfterFailure(t *testing.T) {
	helper := chatTorHelper(t)
	dir, keys := filepath.Join(t.TempDir(), "профиль с пробелами"), t.TempDir()
	chatProfile(t, dir, keys, "alice")
	path := filepath.Join(dir, torconfig.FileName)
	config, err := json.Marshal(map[string]any{"version": 1, "mode": "direct", "tor_binary": helper, "startup_timeout": "1s"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, config, 0600); err != nil {
		t.Fatal(err)
	}
	env := []string{"SHADOWTALK_FAKE_TOR=bootstrap-warning", "SHADOWTALK_FAKE_REGISTRY=" + t.TempDir()}
	p := startTerminalArgs(t, dir, keys, false, false, false, env, "chat")
	p.expect(0, "14%")
	p.expect(0, "Не удалось открыть чат")
	if strings.Contains(p.snapshot(), "PRIVATE") {
		t.Fatal("raw Tor warning reached terminal")
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"mode":"obfs4","bridges":["PRIVATE"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	mark := p.send("r")
	p.expect(mark, "Неверная конфигурация Tor")
	p.send("\x03")
	p.finish(false)

	// A subsequent launch picks up saved obfs4 settings without extra flags.
	bridge := "obfs4 127.0.0.1:1 " + strings.Repeat("A", 40) + " cert=" + base64.RawStdEncoding.EncodeToString(make([]byte, 52)) + " iat-mode=0"
	config, err = json.Marshal(map[string]any{"version": 1, "mode": "obfs4", "tor_binary": helper, "lyrebird_binary": helper, "bridges": []string{bridge}, "startup_timeout": "10s"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, config, 0600); err != nil {
		t.Fatal(err)
	}
	env[0] = "SHADOWTALK_FAKE_TOR=normal"
	restarted := startTerminalArgs(t, dir, keys, false, false, false, env, "chat")
	restarted.expect(0, "Tor готов")
	restarted.send("\x03")
	restarted.finish(true)
	paths, err := filepath.Glob(filepath.Join(dir, "tor", ".shadowtalk-run-*"))
	if err != nil || len(paths) != 0 {
		t.Fatal("runtime remained after obfs4 chat exit", err)
	}
}
