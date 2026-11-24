// Package delivery persists outgoing messages and coordinates retries and ACKs
// over authenticated chat events. It does not depend on SQL, Tor or a UI.
package delivery

import (
	"context"
	"errors"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
)

var (
	ErrInvalidConfig = errors.New("delivery: invalid configuration")
	ErrClosed        = errors.New("delivery: service closed")
	ErrNetwork       = errors.New("delivery: event source stopped or invalid")
	ErrUnknownPeer   = errors.New("delivery: unknown confirmed peer")
	ErrQueueFull     = errors.New("delivery: outgoing queue is full")
)

const MaxPendingMessages = 10000
const MaxBatchSize = 64

type State uint8

const (
	Pending State = iota + 1
	Delivered
)

// Item holds an outgoing history record and its durable delivery state.
// Delivered is the authenticated peer's storage claim, not proof of reading.
type Item struct {
	history.Entry
	State       State
	Attempts    int64
	NextAttempt time.Time
	DeliveredAt time.Time
}

// Store operations must honor context, be concurrent-safe and commit atomically.
// One delivery service owns a store. History-only outgoing records are not
// automatically queued, including those created by older schema versions.
type Store interface {
	history.Store
	Enqueue(context.Context, history.Message) (Item, bool, error)
	Status(context.Context, [32]byte, protocol.MessageID) (Item, error)
	// Due returns at most limit oldest pending messages, one per peer. Later
	// messages to that peer wait for its head's ACK. Sort by due time, then ID
	// sequence so an offline peer cannot monopolize the global batch.
	Due(context.Context, time.Time, int) ([]Item, error)
	// BeginAttempt durably increments attempts and sets a recovery deadline
	// before any bytes are sent. False means the row is no longer due/pending.
	BeginAttempt(context.Context, int64, time.Time, time.Time) (Item, bool, error)
	// Retry updates a pending row only if attempts still equals the supplied
	// value. A racing ACK must never be overwritten.
	Retry(context.Context, int64, int64, time.Time) error
	// Acknowledge matches peer+ID to a queued outgoing row with an attempt.
	// Unknown, unsent and already delivered IDs are ignored (false, nil).
	Acknowledge(context.Context, [32]byte, protocol.MessageID, time.Time) (bool, error)
}

// Network exclusively supplies authenticated chat events. It must support
// concurrent calls; Send/ACK honor ctx, and Close unblocks pending operations.
// HasPeer uses the confirmed contact snapshot. Acknowledge must not dial: if
// the live dialog is gone, the sender's retry will elicit another ACK.
type Network interface {
	HasPeer([32]byte) bool
	Send(context.Context, [32]byte, chat.Message) error
	Acknowledge(context.Context, [32]byte, protocol.MessageID) error
	Disconnect([32]byte) error
	Events() <-chan chat.Event
	Err() error
	Close() error
}

type Options struct {
	MaxWorkers      int           // default 4; maximum 32, at most one active send per peer
	PollInterval    time.Duration // default 1s
	RetryMin        time.Duration // default 5s
	RetryMax        time.Duration // default 5m
	AttemptTimeout  time.Duration // default 90s, including connect and write
	AckTimeout      time.Duration // default 30s before retrying a successful write
	AckWriteTimeout time.Duration // default 5s, bounded receive-loop stall
}

func (o Options) Validate() error { _, err := o.defaults(); return err }
func (o Options) defaults() (Options, error) {
	if o.MaxWorkers < 0 || o.MaxWorkers > 32 {
		return o, ErrInvalidConfig
	}
	for _, d := range []time.Duration{o.PollInterval, o.RetryMin, o.RetryMax, o.AttemptTimeout, o.AckTimeout, o.AckWriteTimeout} {
		if d < 0 || d > 24*time.Hour {
			return o, ErrInvalidConfig
		}
	}
	if o.MaxWorkers == 0 {
		o.MaxWorkers = 4
	}
	if o.PollInterval == 0 {
		o.PollInterval = time.Second
	}
	if o.RetryMin == 0 {
		o.RetryMin = 5 * time.Second
	}
	if o.RetryMax == 0 {
		o.RetryMax = 5 * time.Minute
	}
	if o.AttemptTimeout == 0 {
		o.AttemptTimeout = 90 * time.Second
	}
	if o.AckTimeout == 0 {
		o.AckTimeout = 30 * time.Second
	}
	if o.AckWriteTimeout == 0 {
		o.AckWriteTimeout = 5 * time.Second
	}
	if o.RetryMin > o.RetryMax || o.PollInterval < time.Millisecond || o.RetryMin < time.Millisecond {
		return o, ErrInvalidConfig
	}
	return o, nil
}

func (o Options) retryDelay(attempts int64) time.Duration {
	delay := o.RetryMin
	for i := int64(1); i < attempts && delay < o.RetryMax; i++ {
		if delay > o.RetryMax/2 {
			return o.RetryMax
		}
		delay *= 2
	}
	return min(delay, o.RetryMax)
}
