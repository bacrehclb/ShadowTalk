package chat

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
)

type Service struct {
	source     Sessions
	options    Options
	ctx        context.Context
	cancel     context.CancelFunc
	events     chan Event
	changes    chan struct{}
	done       chan struct{}
	once       sync.Once
	workers    sync.WaitGroup
	mu         sync.Mutex
	closed     bool
	err        error
	closeErr   error
	byKey      map[[32]byte]*Dialog
	byAlias    map[string]*Dialog
	connecting map[string]*attempt
}

// Start takes ownership of source on success. ctx controls the whole service.
// Invalid configuration leaves source untouched for the caller to close.
func Start(ctx context.Context, source Sessions, options Options) (*Service, error) {
	if ctx == nil || source == nil {
		return nil, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	options, err := options.defaults()
	if err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	s := &Service{
		source: source, options: options, ctx: lifetime, cancel: cancel,
		events: make(chan Event, options.EventBuffer), changes: make(chan struct{}, 1), done: make(chan struct{}),
		byKey: make(map[[32]byte]*Dialog), byAlias: make(map[string]*Dialog), connecting: make(map[string]*attempt),
	}
	s.workers.Add(1)
	go s.acceptLoop()
	go func() { <-lifetime.Done(); s.stop(lifetime.Err()) }()
	return s, nil
}

// Events has one consumer. Queued events can be drained after shutdown. A close
// can abort frames still being read or waiting for queue space; there is no ACK.
func (s *Service) Events() <-chan Event { return s.events }

// Changes coalesces notifications to refresh Dialogs. It is a state-change
// signal, not a history of transitions. Both channels close before Done.
func (s *Service) Changes() <-chan struct{} { return s.changes }
func (s *Service) Done() <-chan struct{}    { return s.done }

func (s *Service) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Dialogs returns current handles in alias order. Retained handles remain safe
// after removal: Done and Err describe their termination, without holding text.
func (s *Service) Dialogs() []*Dialog {
	s.mu.Lock()
	result := make([]*Dialog, 0, len(s.byKey))
	for _, dialog := range s.byKey {
		result = append(result, dialog)
	}
	s.mu.Unlock()
	slices.SortFunc(result, func(a, b *Dialog) int { return strings.Compare(a.peer.Alias, b.peer.Alias) })
	return result
}

func (s *Service) Close() error {
	s.stop(nil)
	<-s.done
	return s.closeErr
}

func (s *Service) stop(cause error) {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed, s.err = true, cause
		s.mu.Unlock()
		s.cancel()
		go func() {
			s.closeErr = s.source.Close()
			s.workers.Wait()
			close(s.events)
			close(s.changes)
			close(s.done)
		}()
	})
}

// Called only by registered workers, before the channels can be closed.
func (s *Service) changed() {
	select {
	case s.changes <- struct{}{}:
	default:
	}
}

func (s *Service) acceptLoop() {
	defer s.workers.Done()
	for {
		session, err := s.source.Accept(s.ctx)
		if err != nil || session == nil {
			cause := s.ctx.Err()
			if cause == nil {
				cause = errors.Join(ErrSource, err)
			}
			s.stop(cause)
			return
		}
		_, _ = s.adopt(session)
	}
}

// adopt owns session even if it rejects it. A dialog keeps at most one stream
// per direction: crossed dials can finish safely without closing a live stream
// that may already carry text. Same-direction duplicates are discarded.
func (s *Service) adopt(session Session) (*Dialog, error) {
	peer := session.Peer()
	if peer.Validate() != nil {
		_ = session.Close()
		return nil, ErrInvalidSession
	}
	s.mu.Lock()
	var err error
	if s.closed || s.ctx.Err() != nil {
		err = errors.Join(net.ErrClosed, s.err)
	} else if previous := s.byAlias[peer.Alias]; previous != nil && previous.peer.PublicKey != peer.PublicKey {
		err = ErrInvalidSession
	}
	dialog := s.byKey[peer.PublicKey]
	if err == nil && dialog != nil && dialog.peer.Alias != peer.Alias {
		err = ErrInvalidSession
	}
	if err == nil && dialog == nil && len(s.byKey) >= s.options.MaxDialogs {
		err = ErrDialogLimit
	}
	if err != nil {
		s.mu.Unlock()
		_ = session.Close()
		return nil, err
	}
	created := dialog == nil
	if created {
		dialog = newDialog(s, peer)
		s.byKey[peer.PublicKey], s.byAlias[peer.Alias] = dialog, dialog
		s.workers.Add(1)
	}
	link, err := dialog.attach(session)
	s.mu.Unlock()
	if created {
		go dialog.run()
	}
	if err != nil || link == nil {
		_ = session.Close()
	}
	if err != nil {
		return nil, err
	}
	if link != nil {
		go dialog.readLoop(link)
	}
	if created {
		s.changed()
	}
	return dialog, nil
}

func (s *Service) remove(dialog *Dialog) {
	s.mu.Lock()
	if s.byKey[dialog.peer.PublicKey] == dialog {
		delete(s.byKey, dialog.peer.PublicKey)
		delete(s.byAlias, dialog.peer.Alias)
	}
	s.mu.Unlock()
	s.changed()
}
