package app

import (
	"context"
	"errors"

	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/peers"
)

// StartChat composes live conversations with the existing profile/contact/Tor
// startup. It does not add persistence, delivery retries or user-facing commands.
func (p *PeerSessions) StartChat(ctx context.Context, peerOptions peers.Options, chatOptions chat.Options) (*chat.Service, error) {
	if err := chatOptions.Validate(); err != nil {
		return nil, err
	}
	manager, err := p.Start(ctx, peerOptions)
	if err != nil {
		return nil, err
	}
	service, err := chat.Start(ctx, chatSessions{manager}, chatOptions)
	if err != nil {
		return nil, errors.Join(err, manager.Close())
	}
	return service, nil
}

type chatSessions struct{ *peers.Manager }

func (s chatSessions) Connect(ctx context.Context, alias string) (chat.Session, error) {
	session, err := s.Manager.Connect(ctx, alias)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (s chatSessions) Accept(ctx context.Context) (chat.Session, error) {
	session, err := s.Manager.Accept(ctx)
	if err != nil {
		return nil, err
	}
	return session, nil
}
