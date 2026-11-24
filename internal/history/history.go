// Package history defines persistent message records independently of chat UI,
// networking, SQLite and delivery acknowledgements.
package history

import (
	"context"
	"errors"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/protocol"
)

var (
	ErrInvalidConfig      = errors.New("history: invalid configuration")
	ErrInvalidMessage     = errors.New("history: invalid message")
	ErrInvalidQuery       = errors.New("history: invalid history query")
	ErrNotFound           = errors.New("history: message not found")
	ErrConflict           = errors.New("history: message ID already has different content")
	ErrClosed             = errors.New("history: store is closed")
	ErrBusy               = errors.New("history: store is in use")
	ErrOwnerMismatch      = errors.New("history: database belongs to another identity")
	ErrDecryption         = errors.New("history: authentication or decryption failed")
	ErrCorrupt            = errors.New("history: invalid database or record")
	ErrUnsupportedVersion = errors.New("history: unsupported schema version")
	ErrForeignDatabase    = errors.New("history: unrecognized database")
	ErrStorage            = errors.New("history: storage operation failed")
	ErrUnsafePath         = errors.New("history: unsafe database path or permissions")
)

type Direction uint8

const (
	Incoming Direction = iota + 1
	Outgoing
)

// Message identifies a record by peer, direction and ID. RecordedAt is local
// observation/creation time, not a timestamp supplied by the remote peer.
type Message struct {
	Peer       [32]byte
	Direction  Direction
	ID         protocol.MessageID
	Text       string
	RecordedAt time.Time
}

func (m Message) Validate() error {
	if m.Peer == ([32]byte{}) || (m.Direction != Incoming && m.Direction != Outgoing) ||
		m.RecordedAt.UTC().Year() < 1970 || m.RecordedAt.UTC().Year() > 9999 {
		return ErrInvalidMessage
	}
	if err := (protocol.Frame{Type: protocol.TypeText, ID: m.ID, Text: m.Text}).Validate(); err != nil {
		return ErrInvalidMessage
	}
	return nil
}

type Entry struct {
	Sequence int64
	Message
}

const DefaultPageSize = 50
const MaxPageSize = 200

// Before is an exclusive sequence cursor; zero requests the latest messages.
// Pages are newest-first by insertion sequence, independent of clock changes.
type Query struct {
	Before int64
	Limit  int
}

func (q Query) Validate() error {
	if q.Before < 0 || q.Limit < 0 || q.Limit > MaxPageSize {
		return ErrInvalidQuery
	}
	return nil
}

type Page struct {
	Entries []Entry
	// NextBefore is the last returned sequence when more older rows exist.
	// Zero means the end; do not feed it back as a cursor to restart paging.
	NextBefore int64
}

type Store interface {
	// Append commits a new row, or returns the original unchanged row and false
	// for identical peer/direction/ID/text. A different text is ErrConflict;
	// retry timestamps never replace the original local timestamp.
	Append(context.Context, Message) (Entry, bool, error)
	Get(context.Context, [32]byte, Direction, protocol.MessageID) (Entry, error)
	List(context.Context, [32]byte, Query) (Page, error)
	Close() error
}
