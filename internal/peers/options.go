// Package peers owns bounded, authenticated connections to confirmed contacts.
// It operates on byte streams; message framing and delivery belong above it.
package peers

import (
	"errors"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/securechannel"
)

var (
	ErrInvalidOptions    = errors.New("invalid peer session options")
	ErrInvalidConfig     = errors.New("invalid peer session configuration")
	ErrOwnerMismatch     = errors.New("contacts belong to a different identity")
	ErrTransportIdentity = errors.New("transport address does not match the local identity")
	ErrConnectionLimit   = errors.New("peer connection limit reached")
	ErrTransport         = errors.New("peer transport stopped")
)

type Options struct {
	// MaxConnections includes outgoing dials, incoming handshakes, queued and
	// delivered sessions. Zero selects 32; the supported range is 1..1024.
	MaxConnections int
	// MaxIncomingHandshakes bounds unauthenticated work. Zero selects the smaller
	// of 8 and MaxConnections. Excess connections are closed immediately.
	MaxIncomingHandshakes int
	// ConnectTimeout covers dialing and TLS together; zero selects 90 seconds.
	// HandshakeTimeout bounds TLS on either side; zero selects 15 seconds.
	ConnectTimeout   time.Duration
	HandshakeTimeout time.Duration
}

func (o Options) Validate() error { _, err := o.defaults(); return err }

func (o Options) defaults() (Options, error) {
	if o.MaxConnections < 0 || o.MaxConnections > 1024 || o.MaxIncomingHandshakes < 0 ||
		o.ConnectTimeout < 0 || o.HandshakeTimeout < 0 {
		return o, ErrInvalidOptions
	}
	if o.MaxConnections == 0 {
		o.MaxConnections = 32
	}
	if o.MaxIncomingHandshakes == 0 {
		o.MaxIncomingHandshakes = min(8, o.MaxConnections)
	}
	if o.MaxIncomingHandshakes > o.MaxConnections {
		return o, ErrInvalidOptions
	}
	if o.ConnectTimeout == 0 {
		o.ConnectTimeout = 90 * time.Second
	}
	if o.HandshakeTimeout == 0 {
		o.HandshakeTimeout = securechannel.DefaultHandshakeTimeout
	}
	return o, nil
}
