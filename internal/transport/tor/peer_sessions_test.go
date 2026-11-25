package tor

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/peers"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
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
	var frames []protocol.Frame
	for _, text := range []string{"Привет через Tor 👋\nВторая строка", strings.Repeat("я", protocol.MaxTextBytes/2)} {
		id, err := protocol.NewMessageID()
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, protocol.Frame{Type: protocol.TypeText, ID: id, Text: text})
	}
	replied := make(chan error, 1)
	go func() {
		reader, writer := protocol.NewReader(server), protocol.NewWriter(server)
		// Exercise explicit ack encoding. Durable storage and the decision to
		// acknowledge belong to delivery and are not implemented by this test.
		for _, want := range frames {
			message, err := reader.ReadFrame()
			if err != nil {
				replied <- err
				return
			}
			if message != want {
				replied <- errors.New("wrong protocol message")
				return
			}
			if err := writer.WriteFrame(protocol.Frame{Type: protocol.TypeAck, ID: message.ID}); err != nil {
				replied <- err
				return
			}
		}
		replied <- nil
	}()
	reader, writer := protocol.NewReader(client), protocol.NewWriter(client)
	for _, frame := range frames {
		if err := writer.WriteFrame(frame); err != nil {
			t.Fatal(err)
		}
		ack, err := reader.ReadFrame()
		if err != nil || ack != (protocol.Frame{Type: protocol.TypeAck, ID: frame.ID}) {
			t.Fatalf("acknowledgement: %+v, %v", ack, err)
		}
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
