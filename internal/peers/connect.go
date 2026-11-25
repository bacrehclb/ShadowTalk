package peers

import (
	"context"
	"errors"
	"net"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

// Connect opens a fresh stream to a confirmed alias and verifies its pinned
// identity. ctx affects setup only; the manager still owns the session lifetime.
// Connections are not deduplicated or retried. Callers must Close each session.
func (m *Manager) Connect(ctx context.Context, alias string) (_ *Session, err error) {
	if ctx == nil {
		return nil, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if identity.ValidateNickname(alias) != nil {
		return nil, contacts.ErrInvalidContact
	}
	contact, known := m.byAlias[alias]
	if !known {
		return nil, contacts.ErrContactNotFound
	}
	entry, err := m.reserve(nil, false)
	if err != nil {
		return nil, err
	}
	defer m.workers.Done()
	defer func() {
		if err != nil {
			_ = entry.close()
		}
	}()
	setup, cancel := context.WithTimeout(ctx, m.options.ConnectTimeout)
	defer cancel()
	stop := context.AfterFunc(m.ctx, cancel)
	defer stop()
	raw, err := m.transport.Dial(setup, contact.Onion)
	if err != nil {
		if raw != nil {
			_ = raw.Close()
		}
		return nil, errors.Join(err, setup.Err())
	}
	if raw == nil {
		return nil, ErrInvalidConfig
	}
	m.mu.Lock()
	closed := m.closed || entry.closed
	if !closed {
		entry.raw = raw
	}
	m.mu.Unlock()
	if closed {
		_ = raw.Close()
		return nil, net.ErrClosed
	}
	connection, err := m.endpoint.Client(setup, raw, contact.PublicKey[:])
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	closed = m.closed || entry.closed
	m.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	return &Session{connection: connection, slot: entry, peer: contact}, nil
}
