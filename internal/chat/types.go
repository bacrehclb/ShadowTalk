// Package chat owns live one-to-one conversations on authenticated sessions.
// It has no UI, persistent history, automatic acknowledgements or delivery queue.
package chat

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
)

var (
	ErrInvalidConfig  = errors.New("chat: invalid configuration")
	ErrInvalidSession = errors.New("chat: invalid authenticated session")
	ErrDialogLimit    = errors.New("chat: conversation limit reached")
	ErrDialogClosed   = errors.New("chat: conversation closed")
	ErrSource         = errors.New("chat: session source stopped")
)

// Session must already be authenticated against a confirmed contact. Close
// must promptly unblock I/O and support concurrent calls, as peers.Session does.
type Session interface {
	net.Conn
	Peer() contacts.Contact
	Incoming() bool
}

// Sessions transfers each successful connection to chat. Failed calls must
// clean up their own connections. Accept and Connect must honor ctx; Close
// unblocks both and is safe alongside other calls. Chat owns this source after
// a successful Start, so no other consumer may Accept or Connect through it.
type Sessions interface {
	Accept(context.Context) (Session, error)
	Connect(context.Context, string) (Session, error)
	Close() error
}

type Options struct {
	// Active dialogs and concurrent connection attempts are each limited to
	// MaxDialogs. Zero selects 32; the maximum is 1024.
	MaxDialogs int
	// Buffered data events; zero selects 128, maximum 4096. A full queue
	// blocks readers, applying backpressure without dropping accepted events.
	EventBuffer    int
	ConnectTimeout time.Duration // default 90s, shared by coalesced Connect calls
	WriteTimeout   time.Duration // default 30s, including waiting for the writer
	FrameTimeout   time.Duration // default 30s after the first byte; idle has no limit
}

func (o Options) Validate() error {
	_, err := o.defaults()
	return err
}

func (o Options) defaults() (Options, error) {
	if o.MaxDialogs < 0 || o.MaxDialogs > 1024 || o.EventBuffer < 0 || o.EventBuffer > 4096 ||
		o.ConnectTimeout < 0 || o.WriteTimeout < 0 || o.FrameTimeout < 0 {
		return o, ErrInvalidConfig
	}
	if o.MaxDialogs == 0 {
		o.MaxDialogs = 32
	}
	if o.EventBuffer == 0 {
		o.EventBuffer = 128
	}
	if o.ConnectTimeout == 0 {
		o.ConnectTimeout = 90 * time.Second
	}
	if o.WriteTimeout == 0 {
		o.WriteTimeout = 30 * time.Second
	}
	if o.FrameTimeout == 0 {
		o.FrameTimeout = 30 * time.Second
	}
	return o, nil
}

type Message struct {
	ID   protocol.MessageID
	Text string
}

// NewMessage allocates an ID once. Retain the result on an ambiguous send
// failure; a future delivery layer must persist it and retry the same ID.
func NewMessage(text string) (Message, error) {
	id, err := protocol.NewMessageID()
	if err != nil {
		return Message{}, err
	}
	m := Message{ID: id, Text: text}
	if err := m.frame().Validate(); err != nil {
		return Message{}, err
	}
	return m, nil
}

func (m Message) frame() protocol.Frame {
	return protocol.Frame{Type: protocol.TypeText, ID: m.ID, Text: m.Text}
}

type EventKind uint8

const (
	MessageReceived EventKind = iota + 1
	AcknowledgementReceived
)

// Event carries untrusted text or a peer's acknowledgement claim. An ack has
// only Message.ID; it is never a verified delivery status. Associate either
// kind with Peer.PublicKey, not a nickname or an ID from another peer.
type Event struct {
	Kind    EventKind
	Peer    contacts.Contact
	Message Message
}
