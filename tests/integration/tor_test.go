package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/peers"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
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
	id, err := protocol.NewMessageID()
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.Frame{Type: protocol.TypeText, ID: id, Text: "Привет через Tor 👋"}
	replyID, err := protocol.NewMessageID()
	if err != nil {
		t.Fatal(err)
	}
	wantReply := protocol.Frame{Type: protocol.TypeText, ID: replyID, Text: "authenticated Tor reply"}
	serverDone := make(chan error, 1)
	go func() {
		conn, err := bob.node.Accept(ctx)
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		if conn.Peer().PublicKey != [32]byte(aliceKeys.PublicKey()) {
			serverDone <- errors.New("unexpected incoming peer")
			return
		}
		_ = conn.SetDeadline(time.Now().Add(time.Minute))
		message, err := protocol.NewReader(conn).ReadFrame()
		if err == nil && message != want {
			err = errors.New("unexpected test message")
		}
		if err == nil {
			// Exercise reply framing without claiming durable storage or ack.
			err = protocol.NewWriter(conn).WriteFrame(wantReply)
		}
		serverDone <- err
	}()
	conn, err := alice.node.Connect(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if conn.Peer().PublicKey != [32]byte(bobKeys.PublicKey()) {
		t.Fatal("unexpected outgoing peer")
	}
	_ = conn.SetDeadline(time.Now().Add(time.Minute))
	if err := protocol.NewWriter(conn).WriteFrame(want); err != nil {
		t.Fatal(err)
	}
	reply, err := protocol.NewReader(conn).ReadFrame()
	if err != nil || reply != wantReply {
		t.Fatalf("reply: %+v, %v", reply, err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	t.Log("framed message exchange over mutually authenticated TLS through the real Tor network succeeded")
}
