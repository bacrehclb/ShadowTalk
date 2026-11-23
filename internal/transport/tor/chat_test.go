package tor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/peers"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
	"github.com/bacrehclb/ShadowTalk/internal/storage/contactsfile"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profiledir"
)

func TestChatExchangesMessagesThroughProfilesAndManagedTor(t *testing.T) {
	options := fakeOptions(t, "normal")
	type participant struct {
		profile  app.LoadedProfile
		contacts *contactsfile.Store
		app      *app.PeerSessions
		options  Options
		chat     *chat.Service
	}
	create := func(nickname string) *participant {
		directory := t.TempDir()
		keyring := sessionTestKeyring{}
		profile, err := app.NewProfiles(profiledir.New(directory), keyring).Create(nickname)
		if err != nil {
			t.Fatal(err)
		}
		p := &participant{profile: profile, contacts: contactsfile.New(directory, profile.Profile.PublicKey), options: options}
		p.options.DataDir = filepath.Join(directory, "tor")
		p.app = app.NewPeerSessions(app.NewProfiles(profiledir.New(directory), keyring), p.contacts,
			func(ctx context.Context, keys identity.KeyPair) (peers.Transport, error) {
				return Start(ctx, keys, p.options)
			})
		return p
	}
	alice, bob := create("alice"), create("bob")
	for _, pair := range [][2]*participant{{alice, bob}, {bob, alice}} {
		local, remote := pair[0], pair[1]
		token, err := contacts.CreateInvitation(remote.profile.Keys, remote.profile.Profile.Nickname, time.Now(), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := app.NewContacts(local.contacts).Import(token, remote.profile.Profile.Nickname, remote.profile.Profile.Fingerprint(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	startChat := func(p *participant) {
		service, err := p.app.StartChat(t.Context(), peers.Options{}, chat.Options{})
		if err != nil {
			t.Fatal(err)
		}
		p.chat = service
		t.Cleanup(func() {
			if err := service.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	startChat(alice)
	startChat(bob)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	out, err := alice.chat.Connect(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	awaitEvent := func(service *chat.Service, want chat.Message, peer [32]byte) {
		select {
		case e := <-service.Events():
			if e.Kind != chat.MessageReceived || e.Message != want || e.Peer.PublicKey != peer {
				t.Fatalf("wrong chat event: %+v", e)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	first, err := chat.NewMessage(strings.Repeat("я", protocol.MaxTextBytes/2))
	if err != nil {
		t.Fatal(err)
	}
	if err := out.Send(ctx, first); err != nil {
		t.Fatal(err)
	}
	awaitEvent(bob.chat, first, alice.profile.Profile.PublicKey)
	dialogs := bob.chat.Dialogs()
	if len(dialogs) != 1 {
		t.Fatal("missing incoming dialog")
	}
	reply, err := chat.NewMessage("Привет обратно 👋\nБез общего сервера")
	if err != nil {
		t.Fatal(err)
	}
	if err := dialogs[0].Send(ctx, reply); err != nil {
		t.Fatal(err)
	}
	awaitEvent(alice.chat, reply, bob.profile.Profile.PublicKey)
	if err := alice.chat.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dialogs[0].Done():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assertRuntimeRemoved(t, alice.options.DataDir)
	startChat(alice) // Same profile and Tor directory after full cleanup.
	out, err = alice.chat.Connect(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := chat.NewMessage("reconnected")
	if err != nil {
		t.Fatal(err)
	}
	if err := out.Send(ctx, resumed); err != nil {
		t.Fatal(err)
	}
	awaitEvent(bob.chat, resumed, alice.profile.Profile.PublicKey)
	for _, p := range []*participant{alice, bob} {
		if err := p.chat.Close(); err != nil {
			t.Fatal(err)
		}
		assertRuntimeRemoved(t, p.options.DataDir)
	}
}
