package peers

import (
	"context"
	"errors"
	"net"
)

func (m *Manager) acceptLoop() {
	defer m.workers.Done()
	for {
		raw, err := m.transport.Accept()
		if err != nil || raw == nil {
			if raw != nil {
				_ = raw.Close()
			}
			cause := m.ctx.Err()
			if cause == nil {
				cause = errors.Join(ErrTransport, err, m.transport.Err())
			}
			m.stop(cause)
			return
		}
		// An empty book cannot admit anyone and needs no certificate work.
		if len(m.peerKeys) == 0 {
			_ = raw.Close()
			continue
		}
		entry, err := m.reserve(raw, true)
		if err != nil {
			_ = raw.Close()
			continue
		}
		go m.authenticateIncoming(entry)
	}
}

func (m *Manager) authenticateIncoming(entry *connectionSlot) {
	defer m.workers.Done()
	defer func() {
		m.mu.Lock()
		m.handshakes--
		m.mu.Unlock()
	}()
	connection, err := m.endpoint.ServerForPeers(m.ctx, entry.raw, m.peerKeys)
	if err != nil {
		_ = entry.close()
		return
	}
	contact, trusted := m.byKey[[32]byte(connection.PeerPublicKey())]
	m.mu.Lock()
	if trusted && !m.closed && !entry.closed {
		session := &Session{connection: connection, slot: entry, peer: contact, incoming: true}
		select {
		case m.incoming <- session:
			m.mu.Unlock()
			return
		default:
			// Fail closed if the queue invariant is ever violated. Its capacity
			// is MaxConnections, and every queued session retains a slot.
		}
	}
	m.mu.Unlock()
	_ = entry.close()
}

// Accept waits for an authenticated incoming session. Canceling ctx cancels
// only this wait, not the manager or any already delivered session.
func (m *Manager) Accept(ctx context.Context) (*Session, error) {
	if ctx == nil {
		return nil, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for {
		m.mu.Lock()
		closed, cause := m.closed, m.err
		m.mu.Unlock()
		if closed {
			return nil, errors.Join(net.ErrClosed, cause)
		}
		select {
		case session := <-m.incoming:
			m.mu.Lock()
			usable := !m.closed && !session.slot.closed
			m.mu.Unlock()
			if usable {
				return session, nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-m.ctx.Done():
			return nil, errors.Join(net.ErrClosed, m.Err())
		}
	}
}
