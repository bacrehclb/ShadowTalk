package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/peers"
)

func TestChatStartupRejectsOptionsBeforeAccessingProfileOrNetwork(t *testing.T) {
	service := app.NewPeerSessions(loadProfileFunc(func() (app.LoadedProfile, error) {
		t.Error("invalid chat options loaded secrets")
		return app.LoadedProfile{}, nil
	}), readContactsFunc(func() (*contacts.Book, error) {
		t.Error("invalid chat options read contacts")
		return nil, nil
	}), func(context.Context, identity.KeyPair) (peers.Transport, error) {
		t.Error("invalid chat options started transport")
		return nil, nil
	})
	if got, err := service.StartChat(t.Context(), peers.Options{}, chat.Options{EventBuffer: -1}); got != nil || !errors.Is(err, chat.ErrInvalidConfig) {
		t.Fatal(err)
	}
}

func TestChatStartupPreservesProfileLoadFailure(t *testing.T) {
	failure := errors.New("identity unavailable")
	service := app.NewPeerSessions(loadProfileFunc(func() (app.LoadedProfile, error) {
		return app.LoadedProfile{}, failure
	}), readContactsFunc(func() (*contacts.Book, error) {
		t.Error("failed identity reached contacts")
		return nil, nil
	}), func(context.Context, identity.KeyPair) (peers.Transport, error) {
		t.Error("failed identity reached network")
		return nil, nil
	})
	if got, err := service.StartChat(t.Context(), peers.Options{}, chat.Options{}); got != nil || !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
