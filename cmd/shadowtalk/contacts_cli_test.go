package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profilefile"
)

func TestCLIProcessesExchangeInvitationsAndPersistContacts(t *testing.T) {
	work, keyDirectory := t.TempDir(), t.TempDir()
	aliceDir, bobDir := filepath.Join(work, "Alice профиль"), filepath.Join(work, "Bob профиль")
	call := func(directory string, denyKeys bool, args ...string) processResult {
		t.Helper()
		env := []string{"GORACE=atexit_sleep_ms=0"}
		if denyKeys {
			env = append(env, "SHADOWTALK_TEST_DENY=1")
		}
		return runCLIProcess(t, work, keyDirectory, env, append([]string{"--profile-dir", directory}, args...)...)
	}
	for _, user := range []struct{ directory, nickname string }{{aliceDir, "Alice_01"}, {bobDir, "Bob_02"}} {
		result := call(user.directory, false, "profile", "create", "--nick", user.nickname)
		if result.code != 0 || result.diagnostic != "" {
			t.Fatalf("create: %+v", result)
		}
	}
	created := call(aliceDir, false, "invite", "create")
	if created.code != 0 || created.diagnostic != "" || strings.Count(created.out, "\n") != 1 {
		t.Fatalf("create invite: %+v", created)
	}
	token := strings.TrimSuffix(created.out, "\n")
	parsed, err := contacts.ParseInvitation(token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	alice, err := profilefile.New(aliceDir).Load()
	if err != nil || alice.Fingerprint() != parsed.Fingerprint() {
		t.Fatal("invitation does not match persistent identity")
	}
	invitePath := filepath.Join(work, "-приглашение с пробелами.invite")
	if err := os.WriteFile(invitePath, []byte(token+"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inspected := call(filepath.Join(work, "missing profile"), true, "invite", "inspect", "--file", invitePath)
	if inspected.code != 0 || inspected.diagnostic != "" || !strings.Contains(inspected.out, alice.Fingerprint()) || !strings.Contains(inspected.out, parsed.Onion) {
		t.Fatalf("inspect: %+v", inspected)
	}
	if _, err := os.Stat(filepath.Join(work, "missing profile")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("inspect created profile")
	}
	noConfirmation := call(bobDir, true, "contacts", "add", "--alias", "alice", "--file", invitePath)
	if noConfirmation.code != 1 || noConfirmation.out != "" {
		t.Fatalf("missing confirmation: %+v", noConfirmation)
	}
	if _, err := os.Stat(filepath.Join(bobDir, "contacts.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("saved unconfirmed contact")
	}
	addArgs := []string{"contacts", "add", "--alias", "alice", "--fingerprint", alice.Fingerprint(), "--file", invitePath}
	added := call(bobDir, true, addArgs...)
	if added.code != 0 || added.diagnostic != "" || !strings.Contains(added.out, alice.Fingerprint()) {
		t.Fatalf("add: %+v", added)
	}
	before, err := os.ReadFile(filepath.Join(bobDir, "contacts.json"))
	if err != nil {
		t.Fatal(err)
	}
	listed := call(bobDir, true, "contacts", "list")
	if listed.code != 0 || listed.diagnostic != "" || listed.out != "alice\t"+alice.Fingerprint()+"\n" {
		t.Fatalf("list after restart: %+v", listed)
	}
	shown := call(bobDir, true, "contacts", "show", "--alias", "alice")
	if shown.code != 0 || !strings.Contains(shown.out, parsed.Onion) {
		t.Fatalf("show: %+v", shown)
	}
	repeated := call(bobDir, true, addArgs...)
	if repeated.code != 0 {
		t.Fatalf("reimport: %+v", repeated)
	}
	after, _ := os.ReadFile(filepath.Join(bobDir, "contacts.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("reimport rewrote pin")
	}

	mallory, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	forged, err := contacts.CreateInvitation(mallory, "Alice_01", time.Now(), contacts.DefaultLifetime)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invitePath, []byte(forged), 0600); err != nil {
		t.Fatal(err)
	}
	forgedInfo, _ := contacts.ParseInvitation(forged, time.Now())
	conflict := call(bobDir, true, "contacts", "add", "--alias", "alice", "--fingerprint", forgedInfo.Fingerprint(), "--file", invitePath)
	if conflict.code != 1 || conflict.out != "" || !strings.Contains(conflict.diagnostic, "другой ключ") {
		t.Fatalf("changed key: %+v", conflict)
	}
	wrongRemoval := call(bobDir, true, "contacts", "remove", "--alias", "alice", "--fingerprint", forgedInfo.Fingerprint())
	if wrongRemoval.code != 1 || wrongRemoval.out != "" {
		t.Fatalf("wrong removal: %+v", wrongRemoval)
	}
	after, _ = os.ReadFile(filepath.Join(bobDir, "contacts.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("failed operation modified contact")
	}
	removed := call(bobDir, true, "contacts", "remove", "--alias", "alice", "--fingerprint", alice.Fingerprint())
	if removed.code != 0 || removed.diagnostic != "" {
		t.Fatalf("remove: %+v", removed)
	}
	empty := call(bobDir, true, "contacts", "list")
	if empty.code != 0 || empty.out != "Контактов нет.\n" || empty.diagnostic != "" {
		t.Fatalf("empty list: %+v", empty)
	}
}

type forbiddenReader struct{}

func (forbiddenReader) Read([]byte) (int, error) { panic("unexpected stdin read") }

func TestContactHelpAndInvalidOptionsDoNotReadInputOrProfile(t *testing.T) {
	for _, args := range [][]string{
		{"invite"}, {"invite", "--help"}, {"invite", "create", "--help"}, {"invite", "inspect", "--help"},
		{"contacts"}, {"contacts", "--help"}, {"contacts", "add", "--help"}, {"contacts", "list", "--help"}, {"contacts", "show", "--help"}, {"contacts", "remove", "--help"},
	} {
		directory := filepath.Join(t.TempDir(), "missing")
		var stdout bytes.Buffer
		err := executeWithInput(append([]string{"--profile-dir", directory}, args...), forbiddenReader{}, &stdout, processTestKeys{deny: true})
		if err != nil && !errors.Is(err, flag.ErrHelp) {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), "Использование:") {
			t.Fatal("missing help")
		}
		if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("help created state")
		}
	}
	for _, args := range [][]string{
		{"invite", "create", "--ttl", "0s"}, {"invite", "create", "--ttl", "1ms"}, {"invite", "create", "--ttl", "169h"},
		{"invite", "create", "--ttl", "SECRET_TOKEN_VALUE"}, {"invite", "create", "extra"}, {"invite", "inspect"}, {"invite", "inspect", "--token", "SECRET_TOKEN_VALUE"},
		{"contacts", "add", "--alias", "bob", "--file", "-"}, {"contacts", "add", "--alias", "../bob", "--fingerprint", "pin", "--file", "-"},
		{"contacts", "show"}, {"contacts", "list", "--alias", "bob"}, {"contacts", "remove", "--alias", "bob"}, {"contacts", "invalid"},
	} {
		directory := filepath.Join(t.TempDir(), "missing")
		var stdout bytes.Buffer
		err := executeWithInput(append([]string{"--profile-dir", directory}, args...), forbiddenReader{}, &stdout, processTestKeys{deny: true})
		if err == nil || stdout.Len() != 0 || strings.Contains(err.Error(), "SECRET_TOKEN_VALUE") {
			t.Fatalf("invalid options: %v", err)
		}
		if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid options created state")
		}
	}
}

func TestInvitationStdinBoundariesAndExpiredInput(t *testing.T) {
	keys, _ := identity.GenerateKeyPair()
	token, _ := contacts.CreateInvitation(keys, "alice", time.Now(), contacts.DefaultLifetime)
	for _, ending := range []string{"", "\n", "\r\n"} {
		var stdout bytes.Buffer
		err := executeWithInput([]string{"invite", "inspect", "--file", "-"}, strings.NewReader(token+ending), &stdout, processTestKeys{deny: true})
		if err != nil || stdout.Len() == 0 {
			t.Fatalf("ending %q: %v", ending, err)
		}
	}
	expired, _ := contacts.CreateInvitation(keys, "alice", time.Now().Add(-48*time.Hour), contacts.DefaultLifetime)
	for _, input := range []string{"", token + "\n\n", token + "\r", token + "\n" + token, strings.Repeat("a", contacts.MaxTokenSize+100), expired, "\x1b[2JSECRET_TOKEN_VALUE"} {
		var stdout bytes.Buffer
		err := executeWithInput([]string{"invite", "inspect", "--file", "-"}, strings.NewReader(input), &stdout, processTestKeys{deny: true})
		if err == nil || stdout.Len() != 0 || strings.Contains(userError(err), "SECRET_TOKEN_VALUE") {
			t.Fatalf("bad token: %v", err)
		}
	}
}

type contactBrokenWriter struct{}

func (contactBrokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestContactOutputFailureIsReported(t *testing.T) {
	keys, _ := identity.GenerateKeyPair()
	token, _ := contacts.CreateInvitation(keys, "alice", time.Now(), contacts.DefaultLifetime)
	err := executeWithInput([]string{"invite", "inspect", "--file", "-"}, strings.NewReader(token), contactBrokenWriter{}, processTestKeys{deny: true})
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}

func TestCLIProcessReadsInvitationFromPipeAndHandlesEOF(t *testing.T) {
	keys, _ := identity.GenerateKeyPair()
	token, _ := contacts.CreateInvitation(keys, "alice", time.Now(), contacts.DefaultLifetime)
	work, keyDir := t.TempDir(), t.TempDir()
	env := []string{"SHADOWTALK_TEST_DENY=1", "GORACE=atexit_sleep_ms=0"}
	valid := runCLIProcessWithInput(t, work, keyDir, env, strings.NewReader(token+"\r\n"), "invite", "inspect", "--file", "-")
	if valid.code != 0 || valid.diagnostic != "" || !strings.Contains(valid.out, "Alice") && !strings.Contains(valid.out, "alice") {
		t.Fatalf("pipe: %+v", valid)
	}
	closed := runCLIProcessWithInput(t, work, keyDir, env, strings.NewReader(""), "invite", "inspect", "--file", "-")
	if closed.code != 1 || closed.out != "" || closed.diagnostic == "" {
		t.Fatalf("EOF: %+v", closed)
	}
}
