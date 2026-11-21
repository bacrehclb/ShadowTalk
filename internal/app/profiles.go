package app

import (
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

// ProfileStore is the persistence contract required by the profile use cases.
// Create must refuse to replace an existing profile.
type ProfileStore interface {
	Create(identity.Profile) error
	Load() (identity.Profile, error)
}

type Profiles struct {
	store ProfileStore
}

func NewProfiles(store ProfileStore) *Profiles {
	return &Profiles{store: store}
}

func (p *Profiles) Create(nickname string) (identity.Profile, error) {
	profile := identity.Profile{
		Nickname:  nickname,
		CreatedAt: time.Now().UTC(),
	}
	if err := profile.Validate(); err != nil {
		return identity.Profile{}, err
	}
	if err := p.store.Create(profile); err != nil {
		return identity.Profile{}, err
	}
	return profile, nil
}

func (p *Profiles) Load() (identity.Profile, error) {
	return p.store.Load()
}
