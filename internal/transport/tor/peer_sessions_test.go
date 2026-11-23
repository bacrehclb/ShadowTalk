package tor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/peers"
	"github.com/bacrehclb/ShadowTalk/internal/storage/contactsfile"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profiledir"
)

// Test-only key store: profiles still use their real encrypted seed files,
// while these tests never touch a developer's OS credentials.
type sessionTestKeyring map[string][]byte

func (k sessionTestKeyring) Get(id string) ([]byte, error) {
	key, ok := k[id]
	if !ok {
		return nil, identity.ErrEncryptionKeyNotFound
	}
	return bytes.Clone(key), nil
}
func (k sessionTestKeyring) Set(id string, key []byte) error { k[id] = bytes.Clone(key); return nil }
func (k sessionTestKeyring) Delete(id string) error          { delete(k, id); return nil }

func TestPeerSessionsReloadProfilesAndConnectThroughManagedTor(t *testing.T) {
	options := fakeOptions(t, "normal")
	type participant struct {
		profiles *app.Profiles
		loaded   app.LoadedProfile
		contacts *contactsfile.Store
		options  Options
		service  *app.PeerSessions
		manager  *peers.Manager
		node     *Node
	}
	create := func(nickname string) *participant {
		directory := t.TempDir()
		keyring := sessionTestKeyring{}
		loaded, err := app.NewProfiles(profiledir.New(directory), keyring).Create(nickname)
		if err != nil {
			t.Fatal(err)
		}
		p := &participant{
			profiles: app.NewProfiles(profiledir.New(directory), keyring),
			loaded:   loaded, contacts: contactsfile.New(directory, loaded.Profile.PublicKey), options: options,
		}
		p.options.DataDir = filepath.Join(directory, "tor")
		p.service = app.NewPeerSessions(p.profiles, p.contacts, func(ctx context.Context, keys identity.KeyPair) (peers.Transport, error) {
			node, err := Start(ctx, keys, p.options)
			p.node = node
			return node, err
		})
		return p
	}
	alice, bob := create("Alice"), create("Bob")
	confirm := func(local, remote *participant, alias string) {
		token, err := contacts.CreateInvitation(remote.loaded.Keys, remote.loaded.Profile.Nickname, time.Now(), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := app.NewContacts(local.contacts).Import(token, alias, remote.loaded.Profile.Fingerprint(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	confirm(alice, bob, "bob")
	confirm(bob, alice, "alice")
	for _, p := range []*participant{alice, bob} {
		manager, err := p.service.Start(t.Context(), peers.Options{})
		if err != nil {
			t.Fatal(err)
		}
		p.manager = manager
		t.Cleanup(func() {
			if err := manager.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	setup, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := alice.manager.Connect(setup, "bob")
	if err != nil {
		t.Fatal(err)
	}
	server, err := bob.manager.Accept(setup)
	if err != nil {
		t.Fatal(err)
	}
	cancel() // Session I/O belongs to the manager, not setup's context.
	if client.Peer().PublicKey != bob.loaded.Profile.PublicKey || server.Peer().PublicKey != alice.loaded.Profile.PublicKey {
		t.Fatal("session did not preserve the saved pins")
	}
	for _, conn := range []*peers.Session{client, server} {
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	replied := make(chan error, 1)
	go func() {
		message := make([]byte, 5)
		_, err := io.ReadFull(server, message)
		if err == nil && string(message) != "hello" {
			err = errors.New("wrong message")
		}
		if err == nil {
			_, err = server.Write([]byte("reply"))
		}
		replied <- err
	}()
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 5)
	if _, err := io.ReadFull(client, reply); err != nil || string(reply) != "reply" {
		t.Fatalf("reply: %q, %v", reply, err)
	}
	if err := <-replied; err != nil {
		t.Fatal(err)
	}

	for _, p := range []*participant{alice, bob} {
		if err := p.manager.Close(); err != nil {
			t.Fatal(err)
		}
		waitDone(t, p.node)
		assertRuntimeRemoved(t, p.options.DataDir)
		// Reopening the same directory proves the process and lock were released.
		restarted, err := p.service.Start(t.Context(), peers.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if restarted.Address() != p.manager.Address() {
			t.Error("restart changed onion identity")
		}
		if err := restarted.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.Write([]byte("closed")); err == nil {
		t.Fatal("manager left delivered session open")
	}
}

func TestPeerSessionStartupPropagatesTorFailure(t *testing.T) {
	keys := testKeys(t)
	book, err := contacts.RestoreBook([32]byte(keys.PublicKey()), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = peers.Start(t.Context(), keys, book, peers.Options{}, func(ctx context.Context) (peers.Transport, error) {
		return Start(ctx, keys, Options{BinaryPath: filepath.Join(t.TempDir(), "missing-tor"), DataDir: t.TempDir()})
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
