package peers

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

type testNetwork struct {
	mu    sync.Mutex
	nodes map[string]*testTransport
	dials int
}

type testTransport struct {
	network      *testNetwork
	address      string
	incoming     chan net.Conn
	closed       chan struct{}
	once         sync.Once
	failure      error // guarded by network.mu
	dialOverride func(context.Context, string) (net.Conn, error)
}

func (n *testNetwork) open(keys identity.KeyPair) OpenTransport {
	return func(ctx context.Context) (Transport, error) {
		address, err := keys.OnionAddress()
		if err != nil {
			return nil, err
		}
		transport := &testTransport{network: n, address: address, incoming: make(chan net.Conn), closed: make(chan struct{})}
		n.mu.Lock()
		if n.nodes == nil {
			n.nodes = make(map[string]*testTransport)
		}
		n.nodes[address] = transport
		n.mu.Unlock()
		return transport, nil
	}
}

func (t *testTransport) Address() string { return t.address }
func (t *testTransport) Accept() (net.Conn, error) {
	select {
	case raw := <-t.incoming:
		return raw, nil
	case <-t.closed:
		return nil, net.ErrClosed
	}
}
func (t *testTransport) Dial(ctx context.Context, address string) (net.Conn, error) {
	if t.dialOverride != nil {
		return t.dialOverride(ctx, address)
	}
	t.network.mu.Lock()
	t.network.dials++
	peer := t.network.nodes[address]
	t.network.mu.Unlock()
	if peer == nil {
		return nil, errors.New("test peer unavailable")
	}
	client, server := net.Pipe()
	select {
	case peer.incoming <- server:
		return client, nil
	case <-ctx.Done():
	case <-t.closed:
	case <-peer.closed:
	}
	_ = client.Close()
	_ = server.Close()
	return nil, errors.Join(net.ErrClosed, ctx.Err())
}
func (t *testTransport) Close() error {
	t.once.Do(func() {
		t.network.mu.Lock()
		if t.network.nodes[t.address] == t {
			delete(t.network.nodes, t.address)
		}
		t.network.mu.Unlock()
		close(t.closed)
	})
	return nil
}
func (t *testTransport) Err() error {
	t.network.mu.Lock()
	defer t.network.mu.Unlock()
	return t.failure
}
func (t *testTransport) fail(err error) {
	t.network.mu.Lock()
	t.failure = err
	t.network.mu.Unlock()
	_ = t.Close()
}

func testKeys(t *testing.T) identity.KeyPair {
	t.Helper()
	keys, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func testContact(t *testing.T, alias string, keys identity.KeyPair) contacts.Contact {
	t.Helper()
	address, err := keys.OnionAddress()
	if err != nil {
		t.Fatal(err)
	}
	return contacts.Contact{Alias: alias, Nickname: alias, PublicKey: [32]byte(keys.PublicKey()), Onion: address, AddedAt: time.Now().UTC()}
}

func testBook(t *testing.T, owner identity.KeyPair, entries ...contacts.Contact) *contacts.Book {
	t.Helper()
	book, err := contacts.RestoreBook([32]byte(owner.PublicKey()), entries)
	if err != nil {
		t.Fatal(err)
	}
	return book
}

func startTestManager(t *testing.T, ctx context.Context, keys identity.KeyPair, book *contacts.Book, options Options, open OpenTransport) *Manager {
	t.Helper()
	if options.HandshakeTimeout == 0 {
		options.HandshakeTimeout = 3 * time.Second
	}
	manager, err := Start(ctx, keys, book, options, open)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	return manager
}

func testPair(t *testing.T, options Options) (*Manager, *Manager) {
	t.Helper()
	network := &testNetwork{}
	aliceKeys, bobKeys := testKeys(t), testKeys(t)
	alice := startTestManager(t, t.Context(), aliceKeys, testBook(t, aliceKeys, testContact(t, "bob", bobKeys)), options, network.open(aliceKeys))
	bob := startTestManager(t, t.Context(), bobKeys, testBook(t, bobKeys, testContact(t, "alice", aliceKeys)), options, network.open(bobKeys))
	return alice, bob
}

func connectTestPair(t *testing.T, alice, bob *Manager) (*Session, *Session) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	outgoing, err := alice.Connect(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = outgoing.Close() })
	incoming, err := bob.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = incoming.Close() })
	return outgoing, incoming
}

func exchange(t *testing.T, client, server *Session) {
	t.Helper()
	for _, session := range []*Session{client, server} {
		if err := session.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() {
		data := make([]byte, 5)
		_, err := io.ReadFull(server, data)
		if err == nil && string(data) != "hello" {
			err = errors.New("wrong payload")
		}
		if err == nil {
			_, err = server.Write([]byte("reply"))
		}
		done <- err
	}()
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 5)
	if _, err := io.ReadFull(client, reply); err != nil || string(reply) != "reply" {
		t.Fatalf("reply: %q, %v", reply, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func await(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not finish")
	}
}

func waitResources(t *testing.T, m *Manager, slots, handshakes int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		actualSlots, actualHandshakes := len(m.entries), m.handshakes
		m.mu.Unlock()
		if actualSlots == slots && actualHandshakes == handshakes {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("resources: slots=%d handshakes=%d, want %d/%d", actualSlots, actualHandshakes, slots, handshakes)
		}
	}
}
