package tor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"

	"golang.org/x/net/proxy"
)

// Dial opens an onion v3 stream on ShadowTalk's fixed virtual port. Context
// cancellation affects connection setup only. Node shutdown closes the stream.
func (n *Node) Dial(ctx context.Context, address string) (net.Conn, error) {
	if err := ValidateAddress(address); err != nil {
		return nil, err
	}
	n.mu.Lock()
	closed := n.closed
	n.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	dialCtx, cancel := context.WithTimeout(ctx, n.dialTime)
	defer cancel()
	stop := context.AfterFunc(n.ctx, cancel)
	defer stop()
	// A fresh SOCKS auth label prevents different streams from sharing a circuit
	// through IsolateSOCKSAuth. It is not the peer's authentication credential.
	var isolation [32]byte
	if _, err := rand.Read(isolation[:]); err != nil {
		return nil, ErrDial
	}
	auth := &proxy.Auth{User: hex.EncodeToString(isolation[:16]), Password: hex.EncodeToString(isolation[16:])}
	dialer, err := proxy.SOCKS5("tcp4", n.socks, auth, &net.Dialer{})
	if err != nil {
		return nil, ErrDial
	}
	// x/net's native ContextDialer cancels both TCP and SOCKS negotiation. No
	// uncancellable goroutine, local onion DNS lookup, env proxy or direct fallback.
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, ErrDial
	}
	raw, err := contextDialer.DialContext(dialCtx, "tcp", net.JoinHostPort(address, virtualPort))
	if err != nil {
		return nil, errors.Join(ErrDial, contextError(dialCtx))
	}
	if err = dialCtx.Err(); err != nil {
		_ = raw.Close()
		return nil, errors.Join(ErrDial, err)
	}
	return n.track(raw)
}
