package app_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/delivery"
	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/peers"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profiledir"
	"github.com/bacrehclb/ShadowTalk/internal/storage/sqlite"
)

// Local byte transport only: peers performs the real pinned TLS handshake,
// chat encodes frames, and delivery stores/ACKs them through actual SQLite.
type pipeHub struct {
	mu    sync.Mutex
	nodes map[string]*pipeNode
}
type pipeNode struct {
	hub      *pipeHub
	address  string
	incoming chan net.Conn
	done     chan struct{}
	once     sync.Once
}

func (h *pipeHub) open(keys identity.KeyPair) (peers.Transport, error) {
	address, err := keys.OnionAddress()
	if err != nil {
		return nil, err
	}
	n := &pipeNode{hub: h, address: address, incoming: make(chan net.Conn), done: make(chan struct{})}
	h.mu.Lock()
	h.nodes[address] = n
	h.mu.Unlock()
	return n, nil
}
func (n *pipeNode) Address() string { return n.address }
func (n *pipeNode) Err() error      { return nil }
func (n *pipeNode) Close() error    { n.once.Do(func() { close(n.done) }); return nil }
func (n *pipeNode) Accept() (net.Conn, error) {
	select {
	case c := <-n.incoming:
		return c, nil
	case <-n.done:
		return nil, net.ErrClosed
	}
}
func (n *pipeNode) Dial(ctx context.Context, address string) (net.Conn, error) {
	n.hub.mu.Lock()
	other := n.hub.nodes[address]
	n.hub.mu.Unlock()
	if other == nil {
		return nil, net.ErrClosed
	}
	a, b := net.Pipe()
	select {
	case other.incoming <- b:
		return a, nil
	case <-ctx.Done():
	case <-other.done:
	case <-n.done:
	}
	a.Close()
	b.Close()
	return nil, net.ErrClosed
}

func eventuallyDelivery(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !check() {
		select {
		case <-deadline.C:
			t.Fatal("delivery condition timed out")
		case <-tick.C:
		}
	}
}

func TestDeliveryComposesProfilesTLSHistoryAndRestart(t *testing.T) {
	keys := newMemoryKeys()
	hub := &pipeHub{nodes: make(map[string]*pipeNode)}
	aliceDir, bobDir := filepath.Join(t.TempDir(), "alice"), filepath.Join(t.TempDir(), "bob")
	aliceProfiles, bobProfiles := app.NewProfiles(profiledir.New(aliceDir), keys), app.NewProfiles(profiledir.New(bobDir), keys)
	alice, err := aliceProfiles.Create("alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := bobProfiles.Create("bob")
	if err != nil {
		t.Fatal(err)
	}
	start := func(dir string, local, remote app.LoadedProfile) *delivery.Service {
		t.Helper()
		address, err := remote.Keys.OnionAddress()
		if err != nil {
			t.Fatal(err)
		}
		book, err := contacts.RestoreBook(local.Profile.PublicKey, []contacts.Contact{{Alias: remote.Profile.Nickname, Nickname: remote.Profile.Nickname, PublicKey: remote.Profile.PublicKey, Onion: address, AddedAt: time.Now()}})
		if err != nil {
			t.Fatal(err)
		}
		profiles := app.NewProfiles(profiledir.New(dir), keys)
		p := app.NewPeerSessions(profiles, readContactsFunc(func() (*contacts.Book, error) { return book, nil }), func(_ context.Context, k identity.KeyPair) (peers.Transport, error) { return hub.open(k) })
		s, err := p.StartDelivery(t.Context(), func(ctx context.Context, owner [32]byte, key []byte) (delivery.Store, error) {
			return sqlite.Open(ctx, dir, owner, key)
		}, peers.Options{}, chat.Options{}, delivery.Options{PollInterval: 10 * time.Millisecond, RetryMin: 10 * time.Millisecond, RetryMax: 50 * time.Millisecond, AttemptTimeout: 3 * time.Second, AckTimeout: 100 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
		return s
	}
	as := start(aliceDir, alice, bob)
	m, err := chat.NewMessage("offline text survives restart 👋")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := as.Queue(t.Context(), bob.Profile.PublicKey, m); err != nil {
		t.Fatal(err)
	}
	eventuallyDelivery(t, func() bool {
		item, err := as.Status(t.Context(), bob.Profile.PublicKey, m.ID)
		return err == nil && item.Attempts > 0
	})
	if err := as.Close(); err != nil {
		t.Fatal(err)
	}
	as = start(aliceDir, alice, bob)
	bs := start(bobDir, bob, alice)
	eventuallyDelivery(t, func() bool {
		item, err := as.Status(t.Context(), bob.Profile.PublicKey, m.ID)
		return err == nil && item.State == delivery.Delivered
	})
	page, err := bs.List(t.Context(), alice.Profile.PublicKey, history.Query{})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != m.ID {
		t.Fatal("TLS delivery history", err)
	}
	reply, err := chat.NewMessage(strings.Repeat("é", protocol.MaxTextBytes/2))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := bs.Queue(t.Context(), alice.Profile.PublicKey, reply); err != nil {
		t.Fatal(err)
	}
	eventuallyDelivery(t, func() bool {
		item, err := bs.Status(t.Context(), alice.Profile.PublicKey, reply.ID)
		return err == nil && item.State == delivery.Delivered
	})
	page, err = as.List(t.Context(), bob.Profile.PublicKey, history.Query{})
	if err != nil || len(page.Entries) != 2 || page.Entries[0].Text != reply.Text {
		t.Fatal("reverse delivery", err)
	}
	if err := as.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bs.Close(); err != nil {
		t.Fatal(err)
	}
	root, err := alice.Keys.HistoryKey()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(root)
	store, err := sqlite.Open(t.Context(), aliceDir, alice.Profile.PublicKey, root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item, err := store.Status(t.Context(), bob.Profile.PublicKey, m.ID)
	if err != nil || item.State != delivery.Delivered {
		t.Fatal("delivered state lost on restart", err)
	}
}

func TestDeliveryStartupCleansStorageAndClearsKeyOnTransportFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	keys := newMemoryKeys()
	profiles := app.NewProfiles(profiledir.New(dir), keys)
	created, err := profiles.Create("alice")
	if err != nil {
		t.Fatal(err)
	}
	book, _ := contacts.RestoreBook(created.Profile.PublicKey, nil)
	failure := errors.New("transport unavailable")
	var calls int
	var borrowed []byte
	p := app.NewPeerSessions(loadProfileFunc(func() (app.LoadedProfile, error) { calls++; return profiles.Load() }), readContactsFunc(func() (*contacts.Book, error) { return book, nil }), func(context.Context, identity.KeyPair) (peers.Transport, error) { return nil, failure })
	open := func(ctx context.Context, owner [32]byte, key []byte) (delivery.Store, error) {
		borrowed = key
		return sqlite.Open(ctx, dir, owner, key)
	}
	if s, err := p.StartDelivery(t.Context(), open, peers.Options{}, chat.Options{}, delivery.Options{}); s != nil || !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if calls != 1 || len(borrowed) != 32 || !bytes.Equal(borrowed, make([]byte, 32)) {
		t.Fatal("startup reread identity or retained the borrowed key")
	}
	root, _ := created.Keys.HistoryKey()
	defer clear(root)
	s, err := sqlite.Open(t.Context(), dir, created.Profile.PublicKey, root)
	if err != nil {
		t.Fatal("failed startup retained history lock", err)
	}
	s.Close()
	if _, err := p.StartDelivery(t.Context(), open, peers.Options{}, chat.Options{}, delivery.Options{MaxWorkers: -1}); !errors.Is(err, delivery.ErrInvalidConfig) {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("invalid options loaded profile")
	}
}
