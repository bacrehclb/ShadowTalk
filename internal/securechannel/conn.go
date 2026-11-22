package securechannel

import (
	"bytes"
	"crypto/ed25519"
	"crypto/tls"
	"net"
	"time"
)

// Conn is an authenticated, encrypted byte stream, not a message transport.
// Callers own its lifetime and set deadlines for application reads and writes.
// It follows net.Conn's concurrency contract and does not reconnect or retry.
type Conn struct {
	connection *tls.Conn
	peer       [ed25519.PublicKeySize]byte
}

var _ net.Conn = (*Conn)(nil)

// PeerPublicKey returns an independent copy of the authenticated peer's key.
func (c *Conn) PeerPublicKey() ed25519.PublicKey { return bytes.Clone(c.peer[:]) }

func (c *Conn) Read(p []byte) (int, error)         { return c.connection.Read(p) }
func (c *Conn) Write(p []byte) (int, error)        { return c.connection.Write(p) }
func (c *Conn) Close() error                       { return c.connection.Close() }
func (c *Conn) LocalAddr() net.Addr                { return c.connection.LocalAddr() }
func (c *Conn) RemoteAddr() net.Addr               { return c.connection.RemoteAddr() }
func (c *Conn) SetDeadline(t time.Time) error      { return c.connection.SetDeadline(t) }
func (c *Conn) SetReadDeadline(t time.Time) error  { return c.connection.SetReadDeadline(t) }
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.connection.SetWriteDeadline(t) }
