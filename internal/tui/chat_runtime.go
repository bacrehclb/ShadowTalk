package tui

import (
	"context"
	"errors"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/delivery"
	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

type ChatEntry struct {
	history.Entry
	State delivery.State // zero: historical outgoing record without delivery state
}
type ChatPage struct {
	Entries    []ChatEntry
	NextBefore int64
}

// ChatSession exposes messages and public metadata, never identity secrets.
// Operations honor ctx; Close is concurrent-safe and waits for owned resources.
type ChatSession interface {
	Profile() identity.Profile
	Contacts() []contacts.Contact
	History(context.Context, [32]byte, history.Query) (ChatPage, error)
	Send(context.Context, [32]byte, chat.Message) error
	Activity() map[[32]byte]uint64
	Changes() <-chan struct{}
	Err() error
	Close() error
}

type ChatStage uint8

const (
	ChatLoadingProfile ChatStage = iota
	ChatOpeningHistory
	ChatConnectingTor
)

// OpenChat owns cleanup on failure. On success the runtime owns the session,
// including a success arriving after the user has requested cancellation.
type OpenChat func(context.Context, func(ChatStage)) (ChatSession, error)
type ChatOptions struct {
	InitialContact string
	DescribeError  func(error) string
}

type chatOpened struct {
	session ChatSession
	err     error
}
type chatChanged struct {
	activity map[[32]byte]uint64
	err      error
	stopped  bool
}
type chatClosed struct{ err error }
type chatHistory struct {
	request uint64
	older   bool
	page    ChatPage
	err     error
}
type chatSent struct {
	peer    [32]byte
	message chat.Message
	err     error
}

// The runner owns commands even when Bubble Tea exits on an I/O error. Close
// cancels and waits for startup/commands before closing the resulting session.
// In particular, a late successful startup cannot leak a Tor process or DB lock.
type chatRuntime struct {
	ctx      context.Context
	cancel   context.CancelFunc
	open     OpenChat
	stages   chan ChatStage
	mu       sync.Mutex
	closing  bool
	session  ChatSession
	workers  sync.WaitGroup
	once     sync.Once
	closeErr error
	openErr  error
}

func newChatRuntime(open OpenChat) *chatRuntime {
	ctx, cancel := context.WithCancel(context.Background())
	return &chatRuntime{ctx: ctx, cancel: cancel, open: open, stages: make(chan ChatStage, 1)}
}
func (r *chatRuntime) begin() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return false
	}
	r.workers.Add(1)
	return true
}
func (r *chatRuntime) openCmd() tea.Msg {
	if !r.begin() {
		return chatOpened{err: context.Canceled}
	}
	defer r.workers.Done()
	s, err := r.open(r.ctx, func(stage ChatStage) {
		select {
		case <-r.stages:
		default:
		}
		select {
		case r.stages <- stage:
		default:
		}
	})
	if err == nil && s == nil {
		err = errors.New("чат не был открыт")
	}
	if err == nil {
		r.mu.Lock()
		r.session = s
		r.mu.Unlock()
	}
	r.mu.Lock()
	r.openErr = err
	r.mu.Unlock()
	return chatOpened{session: s, err: err}
}
func (r *chatRuntime) stageCmd() tea.Msg {
	if !r.begin() {
		return nil
	}
	defer r.workers.Done()
	select {
	case stage := <-r.stages:
		return stage
	case <-r.ctx.Done():
		return nil
	}
}
func (r *chatRuntime) watch(s ChatSession) tea.Cmd {
	return func() tea.Msg {
		if !r.begin() {
			return nil
		}
		defer r.workers.Done()
		select {
		case _, ok := <-s.Changes():
			return chatChanged{activity: s.Activity(), err: s.Err(), stopped: !ok}
		case <-r.ctx.Done():
			return nil
		}
	}
}
func (r *chatRuntime) history(s ChatSession, peer [32]byte, q history.Query, request uint64) (tea.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(r.ctx)
	return func() tea.Msg {
		defer cancel()
		if !r.begin() {
			return chatHistory{request: request, err: context.Canceled}
		}
		defer r.workers.Done()
		page, err := s.History(ctx, peer, q)
		return chatHistory{request: request, older: q.Before != 0, page: page, err: err}
	}, cancel
}
func (r *chatRuntime) send(s ChatSession, peer [32]byte, m chat.Message) tea.Cmd {
	return func() tea.Msg {
		if !r.begin() {
			return chatSent{peer: peer, message: m, err: context.Canceled}
		}
		defer r.workers.Done()
		return chatSent{peer: peer, message: m, err: s.Send(r.ctx, peer, m)}
	}
}
func (r *chatRuntime) Close() error {
	r.once.Do(func() {
		r.mu.Lock()
		r.closing = true
		r.cancel()
		r.mu.Unlock()
		r.workers.Wait()
		if r.openErr != nil && !errors.Is(r.openErr, context.Canceled) {
			r.closeErr = r.openErr
		}
		if r.session != nil {
			r.closeErr = errors.Join(r.closeErr, r.session.Close())
		}
	})
	return r.closeErr
}
func (r *chatRuntime) closeCmd() tea.Msg { return chatClosed{err: r.Close()} }
