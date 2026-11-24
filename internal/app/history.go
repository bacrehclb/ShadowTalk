package app

import (
	"bytes"
	"context"

	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

// HistoryFactory opens one profile's store. It must copy/derive from key before
// returning; the caller clears it. Failed opens clean up their own resources.
type HistoryFactory func(ctx context.Context, owner [32]byte, key []byte) (history.Store, error)

type History struct {
	profiles ProfileLoader
	open     HistoryFactory
}

func NewHistory(profiles ProfileLoader, open HistoryFactory) *History {
	return &History{profiles: profiles, open: open}
}

// Open loads an existing identity without creating or replacing profile data.
// It does not subscribe to chat events or imply that messages were delivered.
func (h *History) Open(ctx context.Context) (history.Store, error) {
	if h == nil || ctx == nil || h.profiles == nil || h.open == nil {
		return nil, history.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	loaded, err := h.profiles.Load()
	if err != nil {
		return nil, err
	}
	if !loaded.Profile.HasIdentity() {
		return nil, identity.ErrInvalidKeyPair
	}
	if !bytes.Equal(loaded.Profile.PublicKey[:], loaded.Keys.PublicKey()) {
		return nil, ErrIdentityMismatch
	}
	key, err := loaded.Keys.HistoryKey()
	if err != nil {
		return nil, err
	}
	defer clear(key)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return h.open(ctx, loaded.Profile.PublicKey, key)
}
