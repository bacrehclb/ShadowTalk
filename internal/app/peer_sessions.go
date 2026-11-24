package app

import (
	"bytes"
	"context"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/peers"
)

type ProfileLoader interface {
	Load() (LoadedProfile, error)
}

type ContactReader interface {
	Read() (*contacts.Book, error)
}

// PeerTransportFactory is supplied by the entry point. In production it calls
// tor.Start with these keys and a profile-specific Tor data directory.
type PeerTransportFactory func(context.Context, identity.KeyPair) (peers.Transport, error)

type PeerSessions struct {
	profiles  ProfileLoader
	contacts  ContactReader
	transport PeerTransportFactory
}

func NewPeerSessions(profiles ProfileLoader, contacts ContactReader, transport PeerTransportFactory) *PeerSessions {
	return &PeerSessions{profiles: profiles, contacts: contacts, transport: transport}
}

// Start loads the existing identity and contact snapshot before starting Tor.
// It never creates a profile, upgrades legacy metadata or establishes new trust.
func (p *PeerSessions) Start(ctx context.Context, options peers.Options) (*peers.Manager, error) {
	loaded, book, err := p.load(ctx)
	if err != nil {
		return nil, err
	}
	return peers.Start(ctx, loaded.Keys, book, options, func(lifetime context.Context) (peers.Transport, error) {
		return p.transport(lifetime, loaded.Keys)
	})
}

// load provides one consistent identity/contact snapshot to composed services.
func (p *PeerSessions) load(ctx context.Context) (LoadedProfile, *contacts.Book, error) {
	if p == nil || ctx == nil || p.profiles == nil || p.contacts == nil || p.transport == nil {
		return LoadedProfile{}, nil, peers.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return LoadedProfile{}, nil, err
	}
	loaded, err := p.profiles.Load()
	if err != nil {
		return LoadedProfile{}, nil, err
	}
	if !loaded.Profile.HasIdentity() {
		return LoadedProfile{}, nil, identity.ErrInvalidKeyPair
	}
	if !bytes.Equal(loaded.Profile.PublicKey[:], loaded.Keys.PublicKey()) {
		return LoadedProfile{}, nil, ErrIdentityMismatch
	}
	if err := ctx.Err(); err != nil {
		return LoadedProfile{}, nil, err
	}
	book, err := p.contacts.Read()
	if err != nil {
		return LoadedProfile{}, nil, err
	}
	if book == nil {
		return LoadedProfile{}, nil, peers.ErrInvalidConfig
	}
	if book.Owner() != loaded.Profile.PublicKey {
		return LoadedProfile{}, nil, peers.ErrOwnerMismatch
	}
	book, err = contacts.RestoreBook(book.Owner(), book.List())
	if err != nil {
		return LoadedProfile{}, nil, err
	}
	return loaded, book, nil
}
