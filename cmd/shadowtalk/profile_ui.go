package main

import (
	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

// profileUI keeps restored private keys out of the UI model and its messages.
// Both interfaces use the same application lifecycle and storage guarantees.
type profileUI struct{ profiles *app.Profiles }

func (p profileUI) Load() (identity.Profile, error) {
	loaded, err := p.profiles.Load()
	return loaded.Profile, err
}

func (p profileUI) Create(nickname string) (identity.Profile, error) {
	loaded, err := p.profiles.Create(nickname)
	return loaded.Profile, err
}

func (p profileUI) Upgrade() (identity.Profile, error) {
	loaded, err := p.profiles.Upgrade()
	return loaded.Profile, err
}

func (p profileUI) Recover() (identity.Profile, error) {
	loaded, err := p.profiles.Recover()
	return loaded.Profile, err
}
