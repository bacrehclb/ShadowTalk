package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/peers"
	"github.com/bacrehclb/ShadowTalk/internal/transport/tor"
)

// Explicit opt-in: two independent Tor processes publish services to the real
// network. They exchange synthetic data with ephemeral identities, never a user
// profile. The ordinary test suite and CI remain offline.
func TestTorSecureChannelLive(t *testing.T) {
	if os.Getenv("SHADOWTALK_TOR_LIVE_TEST") != "1" {
		t.Skip("set SHADOWTALK_TOR_LIVE_TEST=1 and optionally SHADOWTALK_TOR_BINARY")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	aliceKeys, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	bobKeys, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		node *peers.Manager
		err  error
	}
	start := func(keys, peerKeys identity.KeyPair, alias string) <-chan result {
		resultCh := make(chan result, 1)
		directory := filepath.Join(t.TempDir(), "tor")
		address, err := peerKeys.OnionAddress()
		if err != nil {
			t.Fatal(err)
		}
		book, err := contacts.RestoreBook([32]byte(keys.PublicKey()), []contacts.Contact{{
			Alias: alias, Nickname: alias, PublicKey: [32]byte(peerKeys.PublicKey()), Onion: address, AddedAt: time.Now().UTC(),
		}})
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			node, err := peers.Start(ctx, keys, book, peers.Options{HandshakeTimeout: time.Minute}, func(lifetime context.Context) (peers.Transport, error) {
				return tor.Start(lifetime, keys, tor.Options{BinaryPath: os.Getenv("SHADOWTALK_TOR_BINARY"), DataDir: directory})
			})
			resultCh <- result{node, err}
		}()
		return resultCh
	}
	aliceCh, bobCh := start(aliceKeys, bobKeys, "bob"), start(bobKeys, aliceKeys, "alice")
	alice, bob := <-aliceCh, <-bobCh
	for _, item := range []result{alice, bob} {
		if item.node != nil {
			defer item.node.Close()
		}
	}
	if err := errors.Join(alice.err, bob.err); err != nil {
		t.Fatal(err)
	}
	t.Log("both Tor nodes bootstrapped and published onion descriptors")
	aliceChat, err := chat.Start(ctx, liveChatSessions{alice.node}, chat.Options{WriteTimeout: time.Minute, FrameTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer aliceChat.Close()
	bobChat, err := chat.Start(ctx, liveChatSessions{bob.node}, chat.Options{WriteTimeout: time.Minute, FrameTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer bobChat.Close()
	outgoing, err := aliceChat.Connect(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	message, err := chat.NewMessage("Привет через Tor 👋")
	if err != nil {
		t.Fatal(err)
	}
	if err := outgoing.Send(ctx, message); err != nil {
		t.Fatal(err)
	}
	receive := func(service *chat.Service, message chat.Message, key [32]byte) {
		select {
		case e := <-service.Events():
			if e.Kind != chat.MessageReceived || e.Message != message || e.Peer.PublicKey != key {
				t.Fatalf("unexpected event: %+v", e)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	receive(bobChat, message, [32]byte(aliceKeys.PublicKey()))
	dialogs := bobChat.Dialogs()
	if len(dialogs) != 1 {
		t.Fatal("missing incoming conversation")
	}
	reply, err := chat.NewMessage("authenticated Tor reply")
	if err != nil {
		t.Fatal(err)
	}
	if err := dialogs[0].Send(ctx, reply); err != nil {
		t.Fatal(err)
	}
	receive(aliceChat, reply, [32]byte(bobKeys.PublicKey()))
	t.Log("bidirectional chat over mutually authenticated TLS through the real Tor network succeeded")
}

// Adapt the concrete peer sessions without changing production interfaces.
type liveChatSessions struct{ *peers.Manager }

func (s liveChatSessions) Connect(ctx context.Context, alias string) (chat.Session, error) {
	session, err := s.Manager.Connect(ctx, alias)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (s liveChatSessions) Accept(ctx context.Context) (chat.Session, error) {
	session, err := s.Manager.Accept(ctx)
	if err != nil {
		return nil, err
	}
	return session, nil
}
