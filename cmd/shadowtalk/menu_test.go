package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profiledir"
	"github.com/bacrehclb/ShadowTalk/internal/storage/torconfig"
)

func TestMenuServicesExchangeInvitationsWithoutCommands(t *testing.T) {
	alice := &menuService{directory: t.TempDir(), keys: make(memoryKeys)}
	bob := &menuService{directory: t.TempDir(), keys: make(memoryKeys)}
	for _, s := range []*menuService{alice, bob} {
		if _, err := app.NewProfiles(profiledir.New(s.directory), s.keys).Create("peer_01"); err != nil {
			t.Fatal(err)
		}
	}
	token, err := alice.CreateInvitation(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	invite, err := contacts.ParseInvitation(token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := bob.ImportContact(token, "alice", "SHA256:wrong"); !errors.Is(err, contacts.ErrFingerprintMismatch) {
		t.Fatal(err)
	}
	if list, err := bob.ListContacts(); err != nil || len(list) != 0 {
		t.Fatal("unconfirmed import saved", err)
	}
	file := filepath.Join(t.TempDir(), "приглашение Alice.invite")
	if err := alice.SaveInvitation(file, token); err != nil {
		t.Fatal(err)
	}
	if err := alice.SaveInvitation(file, token); !errors.Is(err, errMenuFileExists) {
		t.Fatal(err)
	}
	read, err := bob.ReadInvitation(file)
	if err != nil || read != token {
		t.Fatal(err)
	}
	if err := bob.ImportContact(read, "alice", invite.Fingerprint()); err != nil {
		t.Fatal(err)
	}
	list, err := bob.ListContacts()
	if err != nil || len(list) != 1 || list[0].Fingerprint() != invite.Fingerprint() {
		t.Fatal(err)
	}
	if err := bob.RemoveContact("alice", "SHA256:wrong"); !errors.Is(err, contacts.ErrFingerprintMismatch) {
		t.Fatal(err)
	}
	if err := bob.RemoveContact("alice", invite.Fingerprint()); err != nil {
		t.Fatal(err)
	}
	if list, _ := bob.ListContacts(); len(list) != 0 {
		t.Fatal("remove did not persist")
	}
}

func TestMenuTorPasteRoundTripAndInvalidInput(t *testing.T) {
	s := &menuService{directory: t.TempDir()}
	settings, err := s.LoadTor()
	if err != nil {
		t.Fatal(err)
	}
	bridge := "obfs4 192.0.2.1:443 " + strings.Repeat("A", 40) + " cert=" + base64.RawStdEncoding.EncodeToString(make([]byte, 52)) + " iat-mode=0"
	settings.Mode = "obfs4"
	settings.Bridges = "\r\nBridge " + bridge + "\r\n"
	settings.Timeout = "5m"
	if err := s.SaveTor(settings); err != nil {
		t.Fatal(err)
	}
	saved, err := torconfig.Read(filepath.Join(s.directory, torconfig.FileName))
	if err != nil || saved.BinaryPath != "tor" || saved.Connection.LyrebirdBinary != "lyrebird" || len(saved.Connection.Bridges) != 1 || saved.Connection.Bridges[0] != bridge {
		t.Fatal("round trip", err)
	}
	settings, err = s.LoadTor()
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(s.directory, torconfig.FileName))
	settings.Bridges = "obfs4 PRIVATE_INVALID"
	if err := s.SaveTor(settings); !errors.Is(err, errMenuBridges) || strings.Contains(menuError(err), "PRIVATE_INVALID") {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(s.directory, torconfig.FileName))
	if !bytes.Equal(before, after) {
		t.Fatal("bad bridges changed config")
	}
	settings.Mode = "direct"
	settings.TorBinary = filepath.Join(s.directory, "bin", "custom tor")
	if err := s.SaveTor(settings); err != nil {
		t.Fatal(err)
	}
	settings, err = s.LoadTor()
	if err != nil || settings.Mode != "direct" || settings.Bridges != "" || settings.TorBinary == "" {
		t.Fatal(err)
	}
}

func TestMenuHelpAndPipeNeverTouchProfile(t *testing.T) {
	for _, args := range [][]string{{"menu", "--help"}, {"menu"}, {}} {
		path := filepath.Join(t.TempDir(), "missing")
		var output bytes.Buffer
		err := executeWithInput(append([]string{"--profile-dir", path}, args...), forbiddenReader{}, &output, processTestKeys{deny: true})
		if len(args) == 1 && err == nil {
			t.Fatal("menu accepted pipe")
		}
		if len(args) != 1 && output.Len() == 0 {
			t.Fatal("missing help")
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("help/menu entry touched profile")
		}
	}
}
