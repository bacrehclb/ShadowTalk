package securechannel

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

type result struct {
	connection *Conn
	err        error
}

func testIdentity(t *testing.T) (identity.KeyPair, *Endpoint) {
	t.Helper()
	keys, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := New(keys, Options{HandshakeTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return keys, endpoint
}

func testPipe(t *testing.T) (*observedConn, *observedConn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return &observedConn{Conn: a}, &observedConn{Conn: b}
}

type observedConn struct {
	net.Conn
	mu       sync.Mutex
	closed   bool
	deadline time.Time
	wire     bytes.Buffer
}

func (c *observedConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.Conn.Close()
}
func (c *observedConn) SetDeadline(deadline time.Time) error {
	c.mu.Lock()
	c.deadline = deadline
	c.mu.Unlock()
	return c.Conn.SetDeadline(deadline)
}
func (c *observedConn) Write(data []byte) (int, error) {
	c.mu.Lock()
	c.wire.Write(data)
	c.mu.Unlock()
	return c.Conn.Write(data)
}
func (c *observedConn) isClosed() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.closed }

func connectPair(ctx context.Context, client, server *Endpoint, a, b net.Conn, expectedServer, expectedClient ed25519.PublicKey) (result, result) {
	serverResult := make(chan result, 1)
	go func() {
		connection, err := server.Server(ctx, b, expectedClient)
		serverResult <- result{connection, err}
	}()
	connection, err := client.Client(ctx, a, expectedServer)
	return result{connection, err}, <-serverResult
}

func TestChannelAuthenticatesEncryptsAndExchangesData(t *testing.T) {
	clientKeys, client := testIdentity(t)
	serverKeys, server := testIdentity(t)
	a, b := testPipe(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pin := serverKeys.PublicKey()
	c, s := connectPair(ctx, client, server, a, b, pin, clientKeys.PublicKey())
	if c.err != nil || s.err != nil {
		t.Fatalf("connect: client=%v server=%v", c.err, s.err)
	}
	// The setup context and its deadline must not control the established stream.
	cancel()
	if !a.deadline.IsZero() || !b.deadline.IsZero() {
		t.Fatal("handshake deadline leaked into session")
	}
	pin[0] ^= 0xff
	public := c.connection.PeerPublicKey()
	public[0] ^= 0xff
	if !bytes.Equal(c.connection.PeerPublicKey(), serverKeys.PublicKey()) || !bytes.Equal(s.connection.PeerPublicKey(), clientKeys.PublicKey()) {
		t.Fatal("wrong or mutable peer identity")
	}
	state := c.connection.connection.ConnectionState()
	if !state.HandshakeComplete || state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != Protocol || state.DidResume {
		t.Fatal("incorrect negotiated channel")
	}
	for _, connection := range []*Conn{c.connection, s.connection} {
		if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	payload := []byte("private message: this payload must only appear inside encrypted TLS records")
	serverDone := make(chan error, 1)
	go func() {
		data := make([]byte, len(payload))
		_, err := io.ReadFull(s.connection, data)
		if err == nil && !bytes.Equal(data, payload) {
			err = errors.New("server received wrong payload")
		}
		if err == nil {
			_, err = s.connection.Write(data)
		}
		serverDone <- err
	}()
	if _, err := c.connection.Write(payload); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len(payload))
	if _, err := io.ReadFull(c.connection, response); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil || !bytes.Equal(response, payload) {
		t.Fatal("echo failed", err)
	}
	for _, transport := range []*observedConn{a, b} {
		transport.mu.Lock()
		leaked := bytes.Contains(transport.wire.Bytes(), payload)
		transport.mu.Unlock()
		if leaked {
			t.Fatal("application payload was written in plaintext")
		}
	}
	if c.connection.LocalAddr() == nil || c.connection.RemoteAddr() == nil {
		t.Fatal("missing connection addresses")
	}
	// A graceful TLS close must unblock the other side's read.
	readDone := make(chan error, 1)
	go func() { _, err := s.connection.Read(make([]byte, 1)); readDone <- err }()
	if err := c.connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-readDone; !errors.Is(err, io.EOF) {
		t.Fatalf("close: %v", err)
	}
}

func TestWrongPeerKeysRejectBothEndsBeforeReturningAChannel(t *testing.T) {
	for _, rejectedBy := range []string{"client", "server"} {
		t.Run(rejectedBy, func(t *testing.T) {
			clientKeys, client := testIdentity(t)
			serverKeys, server := testIdentity(t)
			stranger, _ := testIdentity(t)
			serverPin, clientPin := serverKeys.PublicKey(), clientKeys.PublicKey()
			if rejectedBy == "client" {
				serverPin = stranger.PublicKey()
			} else {
				clientPin = stranger.PublicKey()
			}
			a, b := testPipe(t)
			c, s := connectPair(context.Background(), client, server, a, b, serverPin, clientPin)
			if c.connection != nil || s.connection != nil || c.err == nil || s.err == nil {
				t.Fatalf("untrusted peer accepted: client=%v server=%v", c.err, s.err)
			}
			ownError := c.err
			if rejectedBy == "server" {
				ownError = s.err
			}
			if !errors.Is(ownError, ErrPeerKeyMismatch) || !errors.Is(ownError, ErrHandshake) {
				t.Fatalf("lost rejection reason: %v", ownError)
			}
			if !a.isClosed() || !b.isClosed() {
				t.Fatal("failed handshake left transport open")
			}
		})
	}
}

func TestSilentPeerAndCancelledContextReleaseTransport(t *testing.T) {
	for _, side := range []string{"client", "server"} {
		for _, mode := range []string{"timeout", "cancel"} {
			t.Run(side+"/"+mode, func(t *testing.T) {
				keys, endpoint := testIdentity(t)
				endpoint.timeout = 100 * time.Millisecond
				a, _ := testPipe(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				want := context.DeadlineExceeded
				if mode == "cancel" {
					cancel()
					want = context.Canceled
				}
				var connection *Conn
				var err error
				if side == "client" {
					connection, err = endpoint.Client(ctx, a, keys.PublicKey())
				} else {
					connection, err = endpoint.Server(ctx, a, keys.PublicKey())
				}
				if connection != nil || !errors.Is(err, want) || !a.isClosed() {
					t.Fatalf("connection=%v err=%v closed=%v", connection, err, a.isClosed())
				}
			})
		}
	}
}

func TestInvalidConfigurationClosesOwnedConnection(t *testing.T) {
	keys, endpoint := testIdentity(t)
	if _, err := New(keys, Options{HandshakeTimeout: -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	if _, err := New(identity.KeyPair{}, Options{}); !errors.Is(err, identity.ErrInvalidKeyPair) {
		t.Fatal(err)
	}
	defaults, err := New(keys, Options{})
	if err != nil || defaults.timeout != DefaultHandshakeTimeout {
		t.Fatal("wrong default timeout", err)
	}
	for _, key := range []ed25519.PublicKey{nil, {}, make([]byte, 31), make([]byte, 32), make([]byte, 33)} {
		a, _ := testPipe(t)
		connection, err := endpoint.Client(context.Background(), a, key)
		if connection != nil || !errors.Is(err, ErrInvalidPeerKey) || !a.isClosed() {
			t.Fatal("invalid pin accepted", err)
		}
	}
	for _, invalid := range []*Endpoint{nil, {}} {
		a, _ := testPipe(t)
		if connection, err := invalid.Server(context.Background(), a, keys.PublicKey()); connection != nil || !errors.Is(err, ErrInvalidConfig) || !a.isClosed() {
			t.Fatal("invalid endpoint accepted")
		}
	}
	a, _ := testPipe(t)
	if _, err := endpoint.Client(nil, a, keys.PublicKey()); !errors.Is(err, ErrInvalidConfig) || !a.isClosed() {
		t.Fatal("nil context accepted")
	}
	if _, err := endpoint.Client(context.Background(), nil, keys.PublicKey()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("nil connection accepted")
	}
}

func TestReadDeadlineAndAbruptDisconnect(t *testing.T) {
	clientKeys, client := testIdentity(t)
	serverKeys, server := testIdentity(t)
	a, b := testPipe(t)
	c, s := connectPair(context.Background(), client, server, a, b, serverKeys.PublicKey(), clientKeys.PublicKey())
	if c.err != nil || s.err != nil {
		t.Fatal(c.err, s.err)
	}
	if err := c.connection.SetReadDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err := c.connection.Read(make([]byte, 1))
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("read deadline ignored: %v", err)
	}
	if err := c.connection.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := c.connection.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_ = b.Close()
	if _, err := c.connection.Read(make([]byte, 1)); err == nil {
		t.Fatal("disconnect not reported")
	}
	if _, err := c.connection.Write([]byte("undelivered")); err == nil {
		t.Fatal("write after disconnect succeeded")
	}
}

func TestEndpointsCanEstablishConcurrentChannels(t *testing.T) {
	clientKeys, client := testIdentity(t)
	serverKeys, server := testIdentity(t)
	var wait sync.WaitGroup
	for range 4 {
		a, b := testPipe(t)
		wait.Go(func() {
			c, s := connectPair(context.Background(), client, server, a, b, serverKeys.PublicKey(), clientKeys.PublicKey())
			if c.err != nil || s.err != nil {
				t.Errorf("concurrent handshake: %v / %v", c.err, s.err)
			}
		})
	}
	wait.Wait()
}
