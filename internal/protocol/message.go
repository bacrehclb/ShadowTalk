// Package protocol frames text messages and storage acknowledgements on an
// already authenticated stream. It does not establish trust, store messages,
// acknowledge them automatically, or render untrusted text in a terminal.
package protocol

import (
	"errors"
	"unicode/utf8"
)

const (
	Version = 1
	// MaxFrameBytes bounds the JSON body, excluding the four-byte length prefix.
	// It accommodates MaxTextBytes even when every byte needs a JSON escape.
	MaxFrameBytes = 128 * 1024
	MaxTextBytes  = 16 * 1024
)

var (
	ErrInvalidConfig      = errors.New("message protocol: missing reader or writer")
	ErrInvalidFrame       = errors.New("message protocol: invalid frame")
	ErrFrameTooLarge      = errors.New("message protocol: frame exceeds size limit")
	ErrUnsupportedVersion = errors.New("message protocol: unsupported version")
	ErrUnsupportedType    = errors.New("message protocol: unsupported frame type")
	ErrInvalidID          = errors.New("message protocol: invalid message ID")
	ErrInvalidText        = errors.New("message protocol: text must be nonempty UTF-8 within the size limit")
)

type Type string

const (
	TypeText Type = "text"
	TypeAck  Type = "ack"
)

// Frame contains application data only. The authenticated session identifies
// its sender. For TypeAck, ID refers to the received text message and Text must
// be empty. An ack means persisted by the peer, not read by a person; only the
// storage/delivery layer can decide when to send it and which outbox it matches.
type Frame struct {
	Type Type
	ID   MessageID
	Text string
}

func (f Frame) Validate() error {
	if f.ID == (MessageID{}) {
		return errors.Join(ErrInvalidFrame, ErrInvalidID)
	}
	switch f.Type {
	case TypeText:
		if len(f.Text) == 0 || len(f.Text) > MaxTextBytes || !utf8.ValidString(f.Text) {
			return errors.Join(ErrInvalidFrame, ErrInvalidText)
		}
	case TypeAck:
		if f.Text != "" {
			return ErrInvalidFrame
		}
	default:
		return errors.Join(ErrInvalidFrame, ErrUnsupportedType)
	}
	return nil
}
