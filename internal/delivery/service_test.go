package delivery_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/delivery"
	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
	"github.com/bacrehclb/ShadowTalk/internal/storage/sqlite"
)

var alice = [32]byte{1}
var bob = [32]byte{2}
var carol = [32]byte{3}

type network struct {
	known      map[[32]byte]bool
	events     chan chat.Event
	closed     chan struct{}
	once       sync.Once
	send       func(context.Context, [32]byte, chat.Message) error
	ack        func(context.Context, [32]byte, protocol.MessageID) error
	disconnect chan [32]byte
}

func newNetwork(peers ...[32]byte) *network {
	n := &network{known: make(map[[32]byte]bool), events: make(chan chat.Event, 32), closed: make(chan struct{}), disconnect: make(chan [32]byte, 8)}
	for _, p := range peers {
		n.known[p] = true
	}
	return n
}
func (n *network) HasPeer(p [32]byte) bool   { return n.known[p] }
func (n *network) Events() <-chan chat.Event { return n.events }
func (n *network) Err() error                { return net.ErrClosed }
func (n *network) Close() error              { n.once.Do(func() { close(n.closed) }); return nil }
func (n *network) Send(ctx context.Context, p [32]byte, m chat.Message) error {
	if n.send != nil {
		return n.send(ctx, p, m)
	}
	return net.ErrClosed
}
func (n *network) Acknowledge(ctx context.Context, p [32]byte, id protocol.MessageID) error {
	if n.ack != nil {
		return n.ack(ctx, p, id)
	}
	return net.ErrClosed
}
func (n *network) Disconnect(p [32]byte) error { n.disconnect <- p; return nil }
func (n *network) emit(ctx context.Context, e chat.Event) error {
	select {
	case n.events <- e:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-n.closed:
		return net.ErrClosed
	}
}

func options() delivery.Options {
	return delivery.Options{MaxWorkers: 2, PollInterval: 5 * time.Millisecond, RetryMin: 20 * time.Millisecond, RetryMax: 80 * time.Millisecond, AttemptTimeout: 100 * time.Millisecond, AckTimeout: 40 * time.Millisecond, AckWriteTimeout: 50 * time.Millisecond}
}
func openStore(t *testing.T, dir string, owner [32]byte) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(t.Context(), dir, owner, bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func start(t *testing.T, s delivery.Store, n delivery.Network) *delivery.Service {
	t.Helper()
	service, err := delivery.Start(t.Context(), s, n, options())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	return service
}
func wait(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !check() {
		select {
		case <-deadline.C:
			t.Fatal("condition did not become true")
		case <-tick.C:
		}
	}
}
func queue(t *testing.T, s *delivery.Service, p [32]byte, text string) chat.Message {
	t.Helper()
	m, err := chat.NewMessage(text)
	if err != nil {
		t.Fatal(err)
	}
	if _, added, err := s.Queue(t.Context(), p, m); err != nil || !added {
		t.Fatal("queue", err)
	}
	return m
}
func delivered(t *testing.T, s *delivery.Service, p [32]byte, m chat.Message) delivery.Item {
	t.Helper()
	var item delivery.Item
	wait(t, func() bool {
		var err error
		item, err = s.Status(t.Context(), p, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		return item.State == delivery.Delivered
	})
	return item
}

func pair(a, b *network, offline *atomic.Bool, dropped *atomic.Bool) {
	a.send = func(ctx context.Context, p [32]byte, m chat.Message) error {
		if offline != nil && offline.Load() {
			return net.ErrClosed
		}
		return b.emit(ctx, chat.Event{Kind: chat.MessageReceived, Peer: contacts.Contact{PublicKey: alice}, Message: m})
	}
	b.ack = func(ctx context.Context, p [32]byte, id protocol.MessageID) error {
		if dropped != nil && !dropped.Swap(true) {
			return net.ErrClosed
		}
		return a.emit(ctx, chat.Event{Kind: chat.AcknowledgementReceived, Peer: contacts.Contact{PublicKey: bob}, Message: chat.Message{ID: id}})
	}
}

func TestDeliveryLostAckRetriesSameMessageWithoutDuplicateHistory(t *testing.T) {
	a, b := newNetwork(bob), newNetwork(alice)
	var dropped atomic.Bool
	pair(a, b, nil, &dropped)
	as := start(t, openStore(t, filepath.Join(t.TempDir(), "alice"), alice), a)
	bs := start(t, openStore(t, filepath.Join(t.TempDir(), "bob"), bob), b)
	m := queue(t, as, bob, "Привет 👋\nwith a lost ACK")
	item := delivered(t, as, bob, m)
	if item.Attempts < 2 {
		t.Fatal("lost ACK did not cause a retry")
	}
	page, err := bs.List(t.Context(), alice, history.Query{})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != m.ID || page.Entries[0].Text != m.Text {
		t.Fatal("duplicate or changed received message", err)
	}
	m2 := queue(t, as, bob, "second message")
	delivered(t, as, bob, m2)
	page, err = bs.List(t.Context(), alice, history.Query{})
	if err != nil || len(page.Entries) != 2 || page.Entries[0].ID != m2.ID {
		t.Fatal("next queued message not released", err)
	}
}

func TestDeliveryResumesOfflineQueueAfterRestart(t *testing.T) {
	aDir, bDir := filepath.Join(t.TempDir(), "alice"), filepath.Join(t.TempDir(), "bob")
	var offline atomic.Bool
	offline.Store(true)
	a, b := newNetwork(bob), newNetwork(alice)
	pair(a, b, &offline, nil)
	as := start(t, openStore(t, aDir, alice), a)
	bs := start(t, openStore(t, bDir, bob), b)
	m := queue(t, as, bob, "survives process restart")
	wait(t, func() bool { item, err := as.Status(t.Context(), bob, m.ID); return err == nil && item.Attempts > 0 })
	if err := as.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bs.Close(); err != nil {
		t.Fatal(err)
	}
	offline.Store(false)
	a, b = newNetwork(bob), newNetwork(alice)
	pair(a, b, &offline, nil)
	as = start(t, openStore(t, aDir, alice), a)
	bs = start(t, openStore(t, bDir, bob), b)
	delivered(t, as, bob, m)
	page, err := bs.List(t.Context(), alice, history.Query{})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != m.ID {
		t.Fatal("restart delivery", err)
	}
}

type failingStore struct {
	delivery.Store
	failure error
}

func (s failingStore) Append(context.Context, history.Message) (history.Entry, bool, error) {
	return history.Entry{}, false, s.failure
}

func TestDeliveryNeverAcknowledgesFailedStorage(t *testing.T) {
	n := newNetwork(bob)
	var acks atomic.Int32
	n.ack = func(context.Context, [32]byte, protocol.MessageID) error { acks.Add(1); return nil }
	failure := errors.New("disk unavailable")
	s := start(t, failingStore{openStore(t, filepath.Join(t.TempDir(), "alice"), alice), failure}, n)
	m, _ := chat.NewMessage("must not ACK")
	n.events <- chat.Event{Kind: chat.MessageReceived, Peer: contacts.Contact{PublicKey: bob}, Message: m}
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("storage failure did not stop service")
	}
	if !errors.Is(s.Err(), failure) || acks.Load() != 0 {
		t.Fatal("failed persistence was acknowledged", s.Err())
	}
}

func TestDeliveryConflictingIncomingIDClosesOnlyItsPeer(t *testing.T) {
	n := newNetwork(bob, carol)
	acknowledgements := make(chan [32]byte, 4)
	n.ack = func(_ context.Context, p [32]byte, _ protocol.MessageID) error { acknowledgements <- p; return nil }
	s := start(t, openStore(t, filepath.Join(t.TempDir(), "alice"), alice), n)
	m, _ := chat.NewMessage("original")
	receive := func(p [32]byte, m chat.Message) {
		t.Helper()
		n.events <- chat.Event{Kind: chat.MessageReceived, Peer: contacts.Contact{PublicKey: p}, Message: m}
	}
	ack := func(want [32]byte) {
		t.Helper()
		select {
		case p := <-acknowledgements:
			if p != want {
				t.Fatal("wrong ACK peer")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no ACK")
		}
	}
	receive(bob, m)
	ack(bob)
	receive(bob, m)
	ack(bob)
	conflict := m
	conflict.Text = "changed"
	receive(bob, conflict)
	select {
	case p := <-n.disconnect:
		if p != bob {
			t.Fatal("wrong disconnected peer")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("conflict did not close peer")
	}
	receive(carol, m)
	ack(carol)
	page, err := s.List(t.Context(), bob, history.Query{})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Text != m.Text {
		t.Fatal("conflict changed history", err)
	}
}

type observedAckStore struct {
	delivery.Store
	processed chan [32]byte
}

func (s observedAckStore) Acknowledge(ctx context.Context, p [32]byte, id protocol.MessageID, now time.Time) (bool, error) {
	ok, err := s.Store.Acknowledge(ctx, p, id, now)
	s.processed <- p
	return ok, err
}

func TestDeliveryIgnoresAckFromAnotherConfirmedPeer(t *testing.T) {
	n := newNetwork(bob, carol)
	processed := make(chan [32]byte, 4)
	s := start(t, observedAckStore{openStore(t, filepath.Join(t.TempDir(), "alice"), alice), processed}, n)
	m := queue(t, s, bob, "ACK must match the peer")
	wait(t, func() bool { item, err := s.Status(t.Context(), bob, m.ID); return err == nil && item.Attempts > 0 })
	n.events <- chat.Event{Kind: chat.AcknowledgementReceived, Peer: contacts.Contact{PublicKey: carol}, Message: chat.Message{ID: m.ID}}
	select {
	case <-processed:
	case <-time.After(5 * time.Second):
		t.Fatal("ACK not processed")
	}
	item, err := s.Status(t.Context(), bob, m.ID)
	if err != nil || item.State != delivery.Pending {
		t.Fatal("wrong peer delivered the message", err)
	}
	n.events <- chat.Event{Kind: chat.AcknowledgementReceived, Peer: contacts.Contact{PublicKey: bob}, Message: chat.Message{ID: m.ID}}
	delivered(t, s, bob, m)
}

func TestDeliveryCloseCancelsBlockedSendWithoutUIConsumer(t *testing.T) {
	n := newNetwork(bob)
	started := make(chan struct{})
	var once sync.Once
	n.send = func(ctx context.Context, _ [32]byte, _ chat.Message) error {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return ctx.Err()
	}
	s := start(t, openStore(t, filepath.Join(t.TempDir(), "alice"), alice), n)
	m := queue(t, s, bob, "blocked network")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("send never started")
	}
	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked")
	}
	if _, _, err := s.Queue(t.Context(), bob, m); !errors.Is(err, delivery.ErrClosed) {
		t.Fatal(err)
	}
}

func TestDeliveryBoundsWorkersAndLetsOtherPeersProgress(t *testing.T) {
	dave := [32]byte{4}
	n := newNetwork(bob, carol, dave)
	started := make(chan [32]byte, 16)
	release := make(chan struct{})
	var active, peak atomic.Int32
	n.send = func(ctx context.Context, p [32]byte, _ chat.Message) error {
		count := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); count > old; old = peak.Load() {
			if peak.CompareAndSwap(old, count) {
				break
			}
		}
		select {
		case started <- p:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case <-release:
			return net.ErrClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	store := openStore(t, filepath.Join(t.TempDir(), "alice"), alice)
	o := options()
	o.AttemptTimeout = 3 * time.Second
	s, err := delivery.Start(t.Context(), store, n, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	queue(t, s, bob, "one")
	queue(t, s, carol, "two")
	queue(t, s, dave, "three")
	seen := make(map[[32]byte]bool)
	for range 2 {
		select {
		case p := <-started:
			seen[p] = true
		case <-time.After(5 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	if len(seen) != 2 || peak.Load() != 2 {
		t.Fatal("worker bound or per-peer exclusion failed")
	}
	select {
	case <-started:
		t.Fatal("third worker bypassed bound")
	default:
	}
	close(release)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for len(seen) < 3 {
		select {
		case p := <-started:
			seen[p] = true
		case <-deadline.C:
			t.Fatal("offline peers starved third peer")
		}
	}
	if peak.Load() > 2 {
		t.Fatal("too many active workers")
	}
}

func TestDeliveryAcceptsFastAckBeforeSendReturns(t *testing.T) {
	n := newNetwork(bob)
	store := openStore(t, filepath.Join(t.TempDir(), "alice"), alice)
	n.send = func(ctx context.Context, p [32]byte, m chat.Message) error {
		item, err := store.Status(ctx, p, m.ID)
		if err != nil || item.Attempts == 0 {
			return errors.New("send preceded durable attempt")
		}
		if err := n.emit(ctx, chat.Event{Kind: chat.AcknowledgementReceived, Peer: contacts.Contact{PublicKey: p}, Message: chat.Message{ID: m.ID}}); err != nil {
			return err
		}
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			item, err = store.Status(ctx, p, m.ID)
			if err != nil {
				return err
			}
			if item.State == delivery.Delivered {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-tick.C:
			}
		}
	}
	s := start(t, store, n)
	m := queue(t, s, bob, "fast ack")
	if item := delivered(t, s, bob, m); item.Attempts != 1 {
		t.Fatal("fast ACK lost")
	}
}
