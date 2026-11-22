package integration

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/securechannel"
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
		node *tor.Node
		err  error
	}
	start := func(keys identity.KeyPair) <-chan result {
		resultCh := make(chan result, 1)
		directory := filepath.Join(t.TempDir(), "tor")
		go func() {
			node, err := tor.Start(ctx, keys, tor.Options{BinaryPath: os.Getenv("SHADOWTALK_TOR_BINARY"), DataDir: directory})
			resultCh <- result{node, err}
		}()
		return resultCh
	}
	aliceCh, bobCh := start(aliceKeys), start(bobKeys)
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
	aliceTLS, err := securechannel.New(aliceKeys, securechannel.Options{HandshakeTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	bobTLS, err := securechannel.New(bobKeys, securechannel.Options{HandshakeTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() {
		raw, err := bob.node.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		conn, err := bobTLS.Server(ctx, raw, aliceKeys.PublicKey())
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Minute))
		message := make([]byte, len("hello over Tor"))
		if _, err = io.ReadFull(conn, message); err == nil && string(message) != "hello over Tor" {
			err = errors.New("unexpected test message")
		}
		if err == nil {
			_, err = conn.Write([]byte("authenticated Tor reply"))
		}
		serverDone <- err
	}()
	raw, err := alice.node.Dial(ctx, bob.node.Address())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := aliceTLS.Client(ctx, raw, bobKeys.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Minute))
	if _, err := conn.Write([]byte("hello over Tor")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, len("authenticated Tor reply"))
	if _, err := io.ReadFull(conn, reply); err != nil || string(reply) != "authenticated Tor reply" {
		t.Fatalf("reply: %q, %v", reply, err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	t.Log("mutually authenticated TLS exchange through the real Tor network succeeded")
}
