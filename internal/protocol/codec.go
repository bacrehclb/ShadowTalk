package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

// Field order and omission are part of v1's canonical JSON representation.
// Keeping this separate from Frame prevents wire details leaking into callers.
type envelope struct {
	Version int    `json:"version"`
	Type    Type   `json:"type"`
	ID      string `json:"id"`
	Text    string `json:"text,omitempty"`
}

func encode(frame Frame) ([]byte, error) {
	if err := frame.Validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(envelope{Version, frame.Type, frame.ID.String(), frame.Text})
	if err != nil {
		return nil, ErrInvalidFrame
	}
	if len(body) > MaxFrameBytes {
		return nil, ErrFrameTooLarge
	}
	return body, nil
}

func decode(body []byte) (Frame, error) {
	if len(body) > MaxFrameBytes {
		return Frame{}, ErrFrameTooLarge
	}
	if !utf8.Valid(body) {
		return Frame{}, ErrInvalidFrame
	}
	var wire envelope
	if err := json.Unmarshal(body, &wire); err != nil {
		// JSON errors can contain peer-controlled values. Do not expose them.
		return Frame{}, ErrInvalidFrame
	}
	if wire.Version != Version {
		return Frame{}, errors.Join(ErrInvalidFrame, ErrUnsupportedVersion)
	}
	id, err := ParseMessageID(wire.ID)
	if err != nil {
		return Frame{}, errors.Join(ErrInvalidFrame, err)
	}
	frame := Frame{Type: wire.Type, ID: id, Text: wire.Text}
	canonical, err := encode(frame)
	if err != nil {
		return Frame{}, err
	}
	// Reject duplicate/unknown/case-folded fields, null, alternate escaping,
	// invalid surrogate pairs and all other ambiguous JSON representations.
	if !bytes.Equal(body, canonical) {
		return Frame{}, ErrInvalidFrame
	}
	return frame, nil
}
