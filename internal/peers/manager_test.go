package peers

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/securechannel"
)

func TestManagersConnectConfirmedPeersAndKeepAnIndependentSnapshot(t *testing.T) {
	network := &testNetwork{}
	aliceKeys, bobKeys, carolKeys := testKeys(t), testKeys(t), testKeys(t)
	book := testBook(t, aliceKeys, testContact(t, "bob", bobKeys), testContact(t, "carol", carolKeys))
	alice := startTestManager(t, t.Context(), aliceKeys, book, Options{}, network.open(aliceKeys))
	bob := startTestManager(t, t.Context(), bobKeys, testBook(t, bobKeys, testContact(t, "alice", aliceKeys)), Options{}, network.open(bobKeys))
	carol := startTestManager(t, t.Context(), carolKeys, testBook(t, carolKeys, testContact(t, "alice", aliceKeys)), Options{}, network.open(carolKeys))
	address, _ := aliceKeys.OnionAddress()
	if alice.Address() != address {
		t.Fatal("incorrect local address")
	}
	confirmed, _ := book.Get("bob")
	if err := book.Remove("bob", confirmed.Fingerprint()); err != nil {
		t.Fatal(err)
	}
	// The running manager deliberately keeps its original trust snapshot.
	for _, remote := range []struct {
		alias string
		node  *Manager
		keys  identity.KeyPair
	}{{"bob", bob, bobKeys}, {"carol", carol, carolKeys}} {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		outgoing, err := alice.Connect(ctx, remote.alias)
		cancel() // Setup cancellation must not invalidate a delivered stream.
		if err != nil {
			t.Fatal(err)
		}
		incoming, err := remote.node.Accept(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if outgoing.Incoming() || !incoming.Incoming() || outgoing.Peer().PublicKey != [32]byte(remote.keys.PublicKey()) || incoming.Peer().PublicKey != [32]byte(aliceKeys.PublicKey()) {
			t.Fatal("wrong peer or direction")
		}
		metadata := outgoing.Peer()
		metadata.Alias = "changed"
		metadata.PublicKey[0] ^= 0xff
		if outgoing.Peer().Alias != remote.alias || outgoing.LocalAddr() == nil || outgoing.RemoteAddr() == nil {
			t.Fatal("session metadata changed")
		}
		exchange(t, outgoing, incoming)
		_ = outgoing.Close()
		_ = incoming.Close()
		await(t, outgoing.Done())
		waitResources(t, alice, 0, 0)
		waitResources(t, remote.node, 0, 0)
	}
}

func TestUnknownAndWrongPeerKeysNeverBecomeSessions(t *testing.T) {
	for _, mode := range []string{"unknown-incoming", "wrong-server-pin", "empty-book"} {
		t.Run(mode, func(t *testing.T) {
			network := &testNetwork{}
			aliceKeys, bobKeys, stranger := testKeys(t), testKeys(t), testKeys(t)
			bobContact := testContact(t, "bob", bobKeys)
			aliceContact := testContact(t, "alice", aliceKeys)
			if mode == "wrong-server-pin" {
				bobContact.PublicKey = [32]byte(stranger.PublicKey())
			}
			if mode == "unknown-incoming" {
				aliceContact = testContact(t, "stranger", stranger)
			}
			bobBook := testBook(t, bobKeys, aliceContact)
			if mode == "empty-book" {
				bobBook = testBook(t, bobKeys)
			}
			alice := startTestManager(t, t.Context(), aliceKeys, testBook(t, aliceKeys, bobContact), Options{}, network.open(aliceKeys))
			bob := startTestManager(t, t.Context(), bobKeys, bobBook, Options{}, network.open(bobKeys))
			conn, err := alice.Connect(t.Context(), "bob")
			if conn != nil || !errors.Is(err, securechannel.ErrHandshake) {
				t.Fatalf("untrusted session returned: %v", err)
			}
			if mode == "wrong-server-pin" && !errors.Is(err, securechannel.ErrPeerKeyMismatch) {
				t.Fatalf("server pin not checked: %v", err)
			}
			waitResources(t, alice, 0, 0)
			waitResources(t, bob, 0, 0)
			if len(bob.incoming) != 0 {
				t.Fatal("untrusted peer reached Accept")
			}
		})
	}
}

func TestMissingContactAndConnectionLimitDoNotDial(t *testing.T) {
	alice, bob := testPair(t, Options{MaxConnections: 1})
	network := alice.transport.(*testTransport).network
	if _, err := alice.Connect(t.Context(), "unknown"); !errors.Is(err, contacts.ErrContactNotFound) {
		t.Fatal(err)
	}
	if _, err := alice.Connect(t.Context(), "bad name"); !errors.Is(err, contacts.ErrInvalidContact) {
		t.Fatal(err)
	}
	if network.dials != 0 {
		t.Fatal("invalid contact caused a network request")
	}
	client, server := connectTestPair(t, alice, bob)
	before := network.dials
	if _, err := alice.Connect(t.Context(), "bob"); !errors.Is(err, ErrConnectionLimit) {
		t.Fatal(err)
	}
	if network.dials != before {
		t.Fatal("capacity rejection still dialed")
	}
	_ = client.Close()
	_ = server.Close()
	waitResources(t, alice, 0, 0)
	waitResources(t, bob, 0, 0)
	client, server = connectTestPair(t, alice, bob)
	exchange(t, client, server)
}

func TestIncomingQueueRetainsCapacityUntilSessionCloses(t *testing.T) {
	alice, bob := testPair(t, Options{MaxConnections: 1})
	first, err := alice.Connect(t.Context(), "bob")
	if err != nil {
		t.Fatal(err)
	}
	waitResources(t, bob, 1, 0)
	_ = first.Close()
	// Bob has not accepted/read the first session and still owns its slot.
	if second, err := alice.Connect(t.Context(), "bob"); second != nil || err == nil {
		t.Fatalf("overfull incoming queue admitted a second session: %v", err)
	}
	queued, err := bob.Accept(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queued.Read(make([]byte, 1)); err == nil {
		t.Fatal("closed peer remained readable")
	}
	await(t, queued.Done())
	client, server := connectTestPair(t, alice, bob)
	exchange(t, client, server)
}

func TestIncomingHandshakeLimitAndTimeoutReleaseSlots(t *testing.T) {
	for _, mode := range []string{"limit", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			options := Options{MaxConnections: 3, MaxIncomingHandshakes: 1}
			if mode == "timeout" {
				options.HandshakeTimeout = 150 * time.Millisecond
			}
			alice, bob := testPair(t, options)
			raw, err := alice.transport.Dial(t.Context(), bob.Address())
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			if mode == "timeout" {
				_ = raw.SetReadDeadline(time.Now().Add(5 * time.Second))
				if _, err := raw.Read(make([]byte, 1)); err == nil {
					t.Fatal("silent peer unexpectedly exchanged data")
				}
				waitResources(t, bob, 0, 0)
				return
			}
			waitResources(t, bob, 1, 1)
			extra, err := alice.transport.Dial(t.Context(), bob.Address())
			if err != nil {
				t.Fatal(err)
			}
			defer extra.Close()
			_ = extra.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := extra.Read(make([]byte, 1)); err == nil {
				t.Fatal("excess handshake was not closed")
			}
			waitResources(t, bob, 1, 1)
			_ = raw.Close()
			waitResources(t, bob, 0, 0)
			client, server := connectTestPair(t, alice, bob)
			exchange(t, client, server)
		})
	}
}

func TestConnectSetupCancellationTimeoutAndShutdown(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			network := &testNetwork{}
			keys, remote := testKeys(t), testKeys(t)
			entered := make(chan struct{})
			open := func(ctx context.Context) (Transport, error) {
				transport, err := network.open(keys)(ctx)
				transport.(*testTransport).dialOverride = func(ctx context.Context, _ string) (net.Conn, error) {
					close(entered)
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return transport, err
			}
			options := Options{}
			if mode == "timeout" {
				options.ConnectTimeout = 100 * time.Millisecond
			}
			manager := startTestManager(t, t.Context(), keys, testBook(t, keys, testContact(t, "remote", remote)), options, open)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			finished := make(chan error, 1)
			go func() { _, err := manager.Connect(ctx, "remote"); finished <- err }()
			await(t, entered)
			want := error(context.Canceled)
			switch mode {
			case "cancel":
				cancel()
			case "timeout":
				want = context.DeadlineExceeded
			case "shutdown":
				if err := manager.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-finished:
				if !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("dial did not stop")
			}
			waitResources(t, manager, 0, 0)
		})
	}
}

func TestSessionReadTimeoutDoesNotReleaseItsSlot(t *testing.T) {
	alice, bob := testPair(t, Options{MaxConnections: 1})
	client, server := connectTestPair(t, alice, bob)
	_ = client.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	_, err := client.Read(make([]byte, 1))
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatal(err)
	}
	select {
	case <-client.Done():
		t.Fatal("application read timeout destroyed the session")
	default:
	}
	if _, err := alice.Connect(t.Context(), "bob"); !errors.Is(err, ErrConnectionLimit) {
		t.Fatal(err)
	}
	exchange(t, client, server)
}

func TestCloseUnblocksIOAcceptAndConcurrentCloseCalls(t *testing.T) {
	alice, bob := testPair(t, Options{})
	client, server := connectTestPair(t, alice, bob)
	read := make(chan error, 1)
	accepted := make(chan error, 1)
	go func() { _, err := client.Read(make([]byte, 1)); read <- err }()
	go func() { _, err := alice.Accept(t.Context()); accepted <- err }()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := alice.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	await(t, alice.Done())
	await(t, client.Done())
	if err := <-read; err == nil {
		t.Fatal("read succeeded after close")
	}
	if err := <-accepted; !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := alice.Connect(t.Context(), "bob"); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	if alice.Err() != nil {
		t.Fatalf("explicit shutdown recorded a failure: %v", alice.Err())
	}
	if _, err := server.Read(make([]byte, 1)); err == nil {
		t.Fatal("remote session did not observe closure")
	}
	await(t, server.Done())
}

func TestAcceptCancellationDoesNotStopManager(t *testing.T) {
	alice, bob := testPair(t, Options{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := bob.Accept(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	client, server := connectTestPair(t, alice, bob)
	exchange(t, client, server)
}

func TestLifetimeCancellationAndTransportFailureCloseQueuedSessions(t *testing.T) {
	for _, mode := range []string{"cancel", "transport-failure"} {
		t.Run(mode, func(t *testing.T) {
			network := &testNetwork{}
			aliceKeys, bobKeys := testKeys(t), testKeys(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			alice := startTestManager(t, t.Context(), aliceKeys, testBook(t, aliceKeys, testContact(t, "bob", bobKeys)), Options{}, network.open(aliceKeys))
			bob := startTestManager(t, ctx, bobKeys, testBook(t, bobKeys, testContact(t, "alice", aliceKeys)), Options{}, network.open(bobKeys))
			client, err := alice.Connect(t.Context(), "bob")
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			want := error(context.Canceled)
			if mode == "cancel" {
				cancel()
			} else {
				want = errors.New("test transport exited")
				bob.transport.(*testTransport).fail(want)
			}
			await(t, bob.Done())
			if !errors.Is(bob.Err(), want) || mode == "transport-failure" && !errors.Is(bob.Err(), ErrTransport) {
				t.Fatalf("lost terminal cause: %v", bob.Err())
			}
			if len(bob.incoming) != 0 {
				t.Fatal("shutdown retained queued sessions")
			}
			if _, err := client.Read(make([]byte, 1)); err == nil {
				t.Fatal("queued connection remained open")
			}
			waitResources(t, alice, 0, 0)
			waitResources(t, bob, 0, 0)
		})
	}
}
