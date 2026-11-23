package protocol

import (
	"crypto/rand"
	"encoding/hex"
)

// MessageID is a nonzero random 128-bit identifier. Generate it once when a
// message is created, persist it with that message, and reuse it for retries.
// Deduplication must also include the authenticated sender's identity.
type MessageID [16]byte

func NewMessageID() (MessageID, error) {
	var id MessageID
	for id == (MessageID{}) {
		if _, err := rand.Read(id[:]); err != nil {
			return MessageID{}, err
		}
	}
	return id, nil
}

func (id MessageID) String() string { return hex.EncodeToString(id[:]) }

// ParseMessageID accepts exactly 32 lowercase hexadecimal characters. The zero
// value is reserved for missing or invalid IDs, never an on-wire identifier.
func ParseMessageID(encoded string) (MessageID, error) {
	var id MessageID
	if len(encoded) != hex.EncodedLen(len(id)) {
		return MessageID{}, ErrInvalidID
	}
	if _, err := hex.Decode(id[:], []byte(encoded)); err != nil || id == (MessageID{}) || id.String() != encoded {
		return MessageID{}, ErrInvalidID
	}
	return id, nil
}
