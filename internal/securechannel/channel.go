// Package securechannel authenticates an existing connection using pinned
// Ed25519 identities and TLS 1.3. It does not dial, listen, or establish trust.
package securechannel

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

const (
	// Protocol is the exact ALPN identifier for this channel and its ready byte.
	Protocol                     = "shadowtalk/secure/1"
	DefaultHandshakeTimeout      = 15 * time.Second
	serverReady             byte = 1
)

var (
	ErrInvalidConfig      = errors.New("invalid secure channel configuration")
	ErrInvalidPeerKey     = errors.New("expected peer must have a nonzero 32-byte Ed25519 public key")
	ErrHandshake          = errors.New("secure channel handshake failed")
	ErrPeerKeyMismatch    = errors.New("peer public key does not match the expected identity")
	ErrInvalidCertificate = errors.New("invalid peer identity certificate")
	ErrProtocol           = errors.New("unsupported secure channel protocol")
)

// Options bounds connection setup independently of later application I/O.
type Options struct {
	// HandshakeTimeout includes TLS and the server's authentication confirmation.
	// Zero selects DefaultHandshakeTimeout; a negative duration is invalid.
	HandshakeTimeout time.Duration
}

// Endpoint holds an immutable signing capability. Reuse it concurrently for
// multiple connections. Each handshake gets a fresh short-lived certificate.
type Endpoint struct {
	signer  crypto.Signer
	timeout time.Duration
}

// New uses an existing identity. It performs no network or storage operations.
func New(keys identity.KeyPair, options Options) (*Endpoint, error) {
	if options.HandshakeTimeout < 0 {
		return nil, fmt.Errorf("%w: negative handshake timeout", ErrInvalidConfig)
	}
	if options.HandshakeTimeout == 0 {
		options.HandshakeTimeout = DefaultHandshakeTimeout
	}
	signer, err := keys.Signer()
	if err != nil {
		return nil, err
	}
	return &Endpoint{signer: signer, timeout: options.HandshakeTimeout}, nil
}

// Client authenticates the accepting peer against expectedPeer and waits for
// confirmation that the peer also authenticated this endpoint.
//
// Client and Server take ownership of raw, including on failure. Pass a fresh,
// unused connection: its deadlines are replaced during setup and cleared on
// success. ctx applies only to setup; canceling it afterward does not close Conn.
func (e *Endpoint) Client(ctx context.Context, raw net.Conn, expectedPeer ed25519.PublicKey) (*Conn, error) {
	return e.establish(ctx, raw, expectedPeer, true)
}

// Server authenticates an incoming peer against expectedPeer. See Client for
// connection ownership and cancellation rules. Unknown peers are not accepted.
func (e *Endpoint) Server(ctx context.Context, raw net.Conn, expectedPeer ed25519.PublicKey) (*Conn, error) {
	return e.establish(ctx, raw, expectedPeer, false)
}

func (e *Endpoint) establish(ctx context.Context, raw net.Conn, expectedPeer ed25519.PublicKey, client bool) (result *Conn, err error) {
	if raw == nil {
		return nil, fmt.Errorf("%w: nil connection", ErrInvalidConfig)
	}
	defer func() {
		if result == nil {
			_ = raw.Close()
		}
	}()
	if e == nil || e.signer == nil || e.timeout <= 0 || ctx == nil {
		return nil, ErrInvalidConfig
	}
	if len(expectedPeer) != ed25519.PublicKeySize || bytes.Equal(expectedPeer, make([]byte, ed25519.PublicKeySize)) {
		return nil, ErrInvalidPeerKey
	}
	peer := bytes.Clone(expectedPeer)
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrHandshake, err)
	}
	certificate, err := newCertificate(e.signer, time.Now())
	if err != nil {
		return nil, fmt.Errorf("create channel certificate: %w", err)
	}
	config := &tls.Config{
		MinVersion:             tls.VersionTLS13,
		MaxVersion:             tls.VersionTLS13,
		Certificates:           []tls.Certificate{certificate},
		NextProtos:             []string{Protocol},
		SessionTicketsDisabled: true,
	}
	usage := x509.ExtKeyUsageClientAuth
	if client {
		usage = x509.ExtKeyUsageServerAuth
		// Trust is the exact Ed25519 pin, not DNS or the system CA store.
		// verifyPeer is mandatory and also validates the certificate itself.
		config.InsecureSkipVerify = true
	} else {
		config.ClientAuth = tls.RequireAnyClientCert
	}
	config.VerifyConnection = func(state tls.ConnectionState) error {
		err := verifyPeer(state, peer, usage, time.Now())
		if err != nil {
			// Reject immediately even if the peer never reads TLS's failure alert.
			// Close the raw transport, not tls.Conn (which holds handshake locks).
			_ = raw.Close()
		}
		return err
	}
	connection := tls.Server(raw, config)
	if client {
		connection = tls.Client(raw, config)
	}
	deadline, _ := ctx.Deadline()
	if err := raw.SetDeadline(deadline); err != nil {
		return nil, errors.Join(ErrHandshake, err)
	}
	// HandshakeContext alone stops watching ctx after TLS finishes. Continue
	// watching while the client waits for the server's authentication decision.
	cancelled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = raw.Close(); close(cancelled) })
	err = handshake(ctx, connection, client)
	if !stop() {
		<-cancelled
	}
	if ctx.Err() != nil {
		return nil, errors.Join(ErrHandshake, ctx.Err(), err)
	}
	if err != nil {
		// A socket deadline may fire just before the context's timer runs.
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() && !time.Now().Before(deadline) {
			err = errors.Join(err, context.DeadlineExceeded)
		}
		return nil, errors.Join(ErrHandshake, err)
	}
	if err := raw.SetDeadline(time.Time{}); err != nil {
		return nil, errors.Join(ErrHandshake, err)
	}
	var publicKey [ed25519.PublicKeySize]byte
	copy(publicKey[:], peer)
	return &Conn{connection: connection, peer: publicKey}, nil
}

func handshake(ctx context.Context, connection *tls.Conn, client bool) error {
	if err := connection.HandshakeContext(ctx); err != nil {
		return err
	}
	if client {
		var confirmation [1]byte
		if _, err := io.ReadFull(connection, confirmation[:]); err != nil {
			return err
		}
		if confirmation[0] != serverReady {
			return ErrProtocol
		}
		return nil
	}
	// TLS 1.3 clients can finish their local handshake before the server checks
	// their certificate. This encrypted byte is sent only after that check.
	_, err := connection.Write([]byte{serverReady})
	return err
}
