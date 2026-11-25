package chat

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

type testSession struct {
	net.Conn
	peer     contacts.Contact
	incoming bool
}

func (s *testSession) Peer() contacts.Contact { return s.peer }
func (s *testSession) Incoming() bool         { return s.incoming }

type testSource struct {
	incoming chan Session
	closed   chan struct{}
	once     sync.Once
	connect  func(context.Context, string) (Session, error)
	dials    atomic.Int64
	mu       sync.Mutex
	failure  error
}

func newSource() *testSource {
	return &testSource{incoming: make(chan Session), closed: make(chan struct{})}
}
func (s *testSource) Accept(ctx context.Context) (Session, error) {
	select {
	case session := <-s.incoming:
		return session, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.closed:
		s.mu.Lock()
		defer s.mu.Unlock()
		return nil, errors.Join(net.ErrClosed, s.failure)
	}
}
func (s *testSource) Connect(ctx context.Context, alias string) (Session, error) {
	s.dials.Add(1)
	if s.connect == nil {
		return nil, contacts.ErrContactNotFound
	}
	return s.connect(ctx, alias)
}
func (s *testSource) Close() error   { s.once.Do(func() { close(s.closed) }); return nil }
func (s *testSource) fail(err error) { s.mu.Lock(); s.failure = err; s.mu.Unlock(); _ = s.Close() }

func contact(t testing.TB, alias string) contacts.Contact {
	t.Helper()
	keys, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	address, err := keys.OnionAddress()
	if err != nil {
		t.Fatal(err)
	}
	return contacts.Contact{Alias: alias, Nickname: alias, PublicKey: [32]byte(keys.PublicKey()), Onion: address, AddedAt: time.Now().UTC()}
}

func pairSources(t *testing.T, beforeDial func(context.Context) error) (*testSource, *testSource) {
	t.Helper()
	alice, bob := newSource(), newSource()
	aliceContact, bobContact := contact(t, "alice"), contact(t, "bob")
	connect := func(local, remote *testSource, localPeer, remotePeer contacts.Contact) func(context.Context, string) (Session, error) {
		return func(ctx context.Context, alias string) (Session, error) {
			if alias != remotePeer.Alias {
				return nil, contacts.ErrContactNotFound
			}
			if beforeDial != nil {
				if err := beforeDial(ctx); err != nil {
					return nil, err
				}
			}
			a, b := net.Pipe()
			out := &testSession{Conn: a, peer: remotePeer}
			in := &testSession{Conn: b, peer: localPeer, incoming: true}
			select {
			case remote.incoming <- in:
				return out, nil
			case <-ctx.Done():
			case <-remote.closed:
			case <-local.closed:
			}
			_ = a.Close()
			_ = b.Close()
			return nil, errors.Join(net.ErrClosed, ctx.Err())
		}
	}
	alice.connect, bob.connect = connect(alice, bob, aliceContact, bobContact), connect(bob, alice, bobContact, aliceContact)
	return alice, bob
}

func start(t *testing.T, source Sessions, options Options) *Service {
	t.Helper()
	service, err := Start(t.Context(), source, options)
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

func await(t testing.TB, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not stop")
	}
}

func connect(t *testing.T, service *Service, alias string) *Dialog {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	dialog, err := service.Connect(ctx, alias)
	if err != nil {
		t.Fatal(err)
	}
	return dialog
}

func findDialog(t *testing.T, service *Service, alias string) *Dialog {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		for _, d := range service.Dialogs() {
			if d.Peer().Alias == alias {
				return d
			}
		}
		select {
		case <-service.Changes():
		case <-service.Done():
			t.Fatalf("service stopped: %v", service.Err())
		case <-timer.C:
			t.Fatalf("dialog %s never appeared", alias)
		}
	}
}

func message(t testing.TB, text string) Message {
	t.Helper()
	m, err := NewMessage(text)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func event(t *testing.T, service *Service) Event {
	t.Helper()
	select {
	case e, ok := <-service.Events():
		if !ok {
			t.Fatalf("event stream closed: %v", service.Err())
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("missing event")
	}
	return Event{}
}

func incoming(t *testing.T, source *testSource, peer contacts.Contact) net.Conn {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	select {
	case source.incoming <- &testSession{Conn: b, peer: peer, incoming: true}:
	case <-time.After(5 * time.Second):
		t.Fatal("incoming session not accepted")
	}
	return a
}
