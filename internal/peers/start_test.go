package peers

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

func TestStartRejectsConfigurationBeforeOpeningTransport(t *testing.T) {
	keys, foreign := testKeys(t), testKeys(t)
	book := testBook(t, keys)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tt := range []struct {
		name    string
		ctx     context.Context
		keys    identity.KeyPair
		book    *contacts.Book
		options Options
		want    error
	}{
		{"nil-context", nil, keys, book, Options{}, ErrInvalidConfig},
		{"canceled", canceled, keys, book, Options{}, context.Canceled},
		{"nil-book", t.Context(), keys, nil, Options{}, ErrInvalidConfig},
		{"invalid-identity", t.Context(), identity.KeyPair{}, book, Options{}, identity.ErrInvalidKeyPair},
		{"foreign-book", t.Context(), keys, testBook(t, foreign), Options{}, ErrOwnerMismatch},
		{"negative-capacity", t.Context(), keys, book, Options{MaxConnections: -1}, ErrInvalidOptions},
		{"excess-capacity", t.Context(), keys, book, Options{MaxConnections: 1025}, ErrInvalidOptions},
		{"negative-handshakes", t.Context(), keys, book, Options{MaxIncomingHandshakes: -1}, ErrInvalidOptions},
		{"excess-handshakes", t.Context(), keys, book, Options{MaxConnections: 1, MaxIncomingHandshakes: 2}, ErrInvalidOptions},
		{"negative-connect-timeout", t.Context(), keys, book, Options{ConnectTimeout: -time.Second}, ErrInvalidOptions},
		{"negative-handshake-timeout", t.Context(), keys, book, Options{HandshakeTimeout: -time.Second}, ErrInvalidOptions},
	} {
		t.Run(tt.name, func(t *testing.T) {
			opened := false
			manager, err := Start(tt.ctx, tt.keys, tt.book, tt.options, func(context.Context) (Transport, error) {
				opened = true
				return nil, nil
			})
			if manager != nil || !errors.Is(err, tt.want) || opened {
				t.Fatalf("manager=%v error=%v opened=%v", manager, err, opened)
			}
		})
	}
	if _, err := Start(t.Context(), keys, book, Options{}, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
}

func TestFailedStartClosesReturnedTransportAndCancelsItsLifetime(t *testing.T) {
	keys := testKeys(t)
	for _, mode := range []string{"wrong-address", "canceled", "nil-transport"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			network := &testNetwork{}
			var transport *testTransport
			var lifetime context.Context
			want := ErrInvalidConfig
			manager, err := Start(ctx, keys, testBook(t, keys), Options{}, func(inner context.Context) (Transport, error) {
				lifetime = inner
				if mode == "nil-transport" {
					want = ErrInvalidConfig
					return nil, nil
				}
				opened, err := network.open(keys)(inner)
				if err != nil {
					return nil, err
				}
				transport = opened.(*testTransport)
				switch mode {
				case "wrong-address":
					transport.address = "wrong"
					want = ErrTransportIdentity
				case "canceled":
					cancel()
					want = context.Canceled
				}
				return transport, nil
			})
			if manager != nil || !errors.Is(err, want) {
				t.Fatalf("manager=%v error=%v, want %v", manager, err, want)
			}
			await(t, lifetime.Done())
			if transport != nil {
				await(t, transport.closed)
			}
		})
	}
}

func TestFactoryErrorDoesNotDereferenceTypedNilTransport(t *testing.T) {
	keys := testKeys(t)
	failure := errors.New("transport unavailable")
	var lifetime context.Context
	_, err := Start(t.Context(), keys, testBook(t, keys), Options{}, func(ctx context.Context) (Transport, error) {
		lifetime = ctx
		var transport *testTransport
		return transport, failure // Common constructor-to-interface conversion.
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	await(t, lifetime.Done())
}

func TestCloseDuringHandshakeAndConcurrentConnects(t *testing.T) {
	alice, bob := testPair(t, Options{})
	// An inbound raw connection that never begins TLS must not delay shutdown.
	raw, err := alice.transport.Dial(t.Context(), bob.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	waitResources(t, bob, 1, 1)
	finished := make(chan error, 12)
	for range 12 {
		go func() {
			session, err := alice.Connect(t.Context(), "bob")
			if session != nil {
				_ = session.Close()
			}
			finished <- err
		}()
	}
	if err := bob.Close(); err != nil {
		t.Fatal(err)
	}
	if err := alice.Close(); err != nil {
		t.Fatal(err)
	}
	for range 12 {
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("connect survived manager shutdown")
		}
	}
	waitResources(t, alice, 0, 0)
	waitResources(t, bob, 0, 0)
	if _, err := bob.Accept(t.Context()); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
}
