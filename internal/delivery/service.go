package delivery

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
)

type Service struct {
	store    Store
	network  Network
	options  Options
	ctx      context.Context
	cancel   context.CancelFunc
	wake     chan struct{}
	changes  chan struct{}
	done     chan struct{}
	once     sync.Once
	workers  sync.WaitGroup
	mu       sync.Mutex
	closed   bool
	err      error
	closeErr error
}

// Start owns store and network on success, including their Close. On failure
// both remain owned by the caller. It exclusively consumes network.Events.
// Existing due records resume automatically. ctx controls the whole service.
func Start(ctx context.Context, store Store, network Network, options Options) (*Service, error) {
	if ctx == nil || store == nil || network == nil || network.Events() == nil {
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
	s := &Service{store: store, network: network, options: options, ctx: lifetime, cancel: cancel, wake: make(chan struct{}, 1), changes: make(chan struct{}, 1), done: make(chan struct{})}
	s.workers.Add(2)
	go s.receive()
	go s.dispatch()
	go func() { <-lifetime.Done(); s.stop(lifetime.Err()) }()
	return s, nil
}

func (s *Service) Done() <-chan struct{} { return s.done }

// Changes coalesces notifications. Consumers refresh List/Status; no message
// payload waits for UI consumption, and shutdown never depends on the UI.
func (s *Service) Changes() <-chan struct{} { return s.changes }
func (s *Service) Err() error               { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

func (s *Service) Close() error { s.stop(nil); <-s.done; return s.closeErr }

func (s *Service) stop(cause error) {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.err = cause
		s.cancel()
		s.mu.Unlock()
		go func() {
			s.closeErr = s.network.Close()
			s.workers.Wait()
			s.closeErr = errors.Join(s.closeErr, s.store.Close())
			close(s.changes)
			close(s.done)
		}()
	})
}

// begin registers public operations before shutdown can close their store.
func (s *Service) begin(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil, ErrClosed
	}
	s.workers.Add(1)
	s.mu.Unlock()
	op, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	return op, func() { stop(); cancel(); s.workers.Done() }, nil
}

// Queue persists before returning; it does not wait for network delivery.
// Create the message once with chat.NewMessage and retain its ID on ambiguous
// errors. Repeating Queue is idempotent and never requeues a delivered message.
func (s *Service) Queue(ctx context.Context, peer [32]byte, message chat.Message) (Item, bool, error) {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return Item{}, false, err
	}
	defer finish()
	if !s.network.HasPeer(peer) {
		return Item{}, false, ErrUnknownPeer
	}
	item, added, err := s.store.Enqueue(ctx, history.Message{Peer: peer, Direction: history.Outgoing, ID: message.ID, Text: message.Text, RecordedAt: time.Now()})
	if err != nil {
		return Item{}, false, err
	}
	if added {
		s.changed()
	}
	s.trigger()
	return item, added, nil
}

func (s *Service) Status(ctx context.Context, peer [32]byte, id protocol.MessageID) (Item, error) {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return Item{}, err
	}
	defer finish()
	return s.store.Status(ctx, peer, id)
}

func (s *Service) List(ctx context.Context, peer [32]byte, query history.Query) (history.Page, error) {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return history.Page{}, err
	}
	defer finish()
	return s.store.List(ctx, peer, query)
}

// Called only by registered operations/workers before changes closes.
func (s *Service) changed() {
	select {
	case s.changes <- struct{}{}:
	default:
	}
}
func (s *Service) trigger() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
