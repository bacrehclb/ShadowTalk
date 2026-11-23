package chat

import (
	"context"
	"errors"
	"net"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

type attempt struct {
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	waiters  int // guarded by Service.mu
	finished bool
	dialog   *Dialog // published by closing done
	err      error
}

// Connect reuses a dialog or coalesces a pending dial for this alias. Canceling
// one waiter does not abort other waiters; canceling the last cancels setup.
// After a successful attachment the dialog belongs to the service lifetime.
func (s *Service) Connect(ctx context.Context, alias string) (*Dialog, error) {
	if ctx == nil {
		return nil, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if identity.ValidateNickname(alias) != nil {
		return nil, contacts.ErrInvalidContact
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, net.ErrClosed
	}
	if dialog := s.byAlias[alias]; dialog != nil {
		s.mu.Unlock()
		if dialog.ctx.Err() != nil {
			return nil, ErrDialogClosed
		}
		return dialog, nil
	}
	pending := s.connecting[alias]
	if pending == nil {
		if len(s.byKey) >= s.options.MaxDialogs || len(s.connecting) >= s.options.MaxDialogs {
			s.mu.Unlock()
			return nil, ErrDialogLimit
		}
		setup, cancel := context.WithTimeout(s.ctx, s.options.ConnectTimeout)
		pending = &attempt{ctx: setup, cancel: cancel, done: make(chan struct{})}
		s.connecting[alias] = pending
		s.workers.Add(1)
		go s.connect(alias, pending)
	}
	pending.waiters++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		pending.waiters--
		if pending.waiters == 0 && !pending.finished {
			pending.cancel()
		}
		s.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, errors.Join(net.ErrClosed, s.Err())
	case <-pending.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return pending.dialog, pending.err
	}
}

func (s *Service) connect(alias string, pending *attempt) {
	defer s.workers.Done()
	defer pending.cancel()
	session, err := s.source.Connect(pending.ctx, alias)
	var dialog *Dialog
	if err == nil {
		if session == nil {
			err = ErrInvalidSession
		} else if pending.ctx.Err() != nil {
			err = pending.ctx.Err()
			_ = session.Close()
		} else if session.Peer().Alias != alias || session.Incoming() {
			err = ErrInvalidSession
			_ = session.Close()
		} else {
			dialog, err = s.adopt(session)
		}
	}
	s.mu.Lock()
	pending.dialog, pending.err, pending.finished = dialog, err, true
	delete(s.connecting, alias)
	close(pending.done)
	s.mu.Unlock()
}
