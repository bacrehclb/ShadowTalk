package app

import (
	"context"
	"errors"

	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/delivery"
	"github.com/bacrehclb/ShadowTalk/internal/peers"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
)

// DeliveryStoreFactory must derive/copy the supplied key before returning and
// clean up failed opens. Production composition supplies storage/sqlite.Open.
type DeliveryStoreFactory func(context.Context, [32]byte, []byte) (delivery.Store, error)

// StartDelivery composes one identity and contact snapshot with persistent
// history/outbox and live chat. It returns only after storage and network startup;
// the service then owns both. No CLI/TUI command is introduced by this API.
func (p *PeerSessions) StartDelivery(ctx context.Context, open DeliveryStoreFactory, peerOptions peers.Options, chatOptions chat.Options, options delivery.Options) (*delivery.Service, error) {
	if open == nil {
		return nil, delivery.ErrInvalidConfig
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if err := peerOptions.Validate(); err != nil {
		return nil, err
	}
	if err := chatOptions.Validate(); err != nil {
		return nil, err
	}
	loaded, book, err := p.load(ctx)
	if err != nil {
		return nil, err
	}
	key, err := loaded.Keys.HistoryKey()
	if err != nil {
		return nil, err
	}
	store, err := open(ctx, loaded.Profile.PublicKey, key)
	clear(key)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, delivery.ErrInvalidConfig
	}
	manager, err := peers.Start(ctx, loaded.Keys, book, peerOptions, func(lifetime context.Context) (peers.Transport, error) { return p.transport(lifetime, loaded.Keys) })
	if err != nil {
		return nil, errors.Join(err, store.Close())
	}
	core, err := chat.Start(ctx, chatSessions{manager}, chatOptions)
	if err != nil {
		return nil, errors.Join(err, manager.Close(), store.Close())
	}
	network := deliveryNetwork{core: core, aliases: make(map[[32]byte]string)}
	for _, contact := range book.List() {
		network.aliases[contact.PublicKey] = contact.Alias
	}
	service, err := delivery.Start(ctx, store, network, options)
	if err != nil {
		return nil, errors.Join(err, core.Close(), store.Close())
	}
	return service, nil
}

type deliveryNetwork struct {
	core    *chat.Service
	aliases map[[32]byte]string // Immutable, same snapshot as the peer manager.
}

func (n deliveryNetwork) HasPeer(peer [32]byte) bool { _, ok := n.aliases[peer]; return ok }
func (n deliveryNetwork) Events() <-chan chat.Event  { return n.core.Events() }
func (n deliveryNetwork) Err() error                 { return n.core.Err() }
func (n deliveryNetwork) Close() error               { return n.core.Close() }
func (n deliveryNetwork) Send(ctx context.Context, peer [32]byte, message chat.Message) error {
	alias, ok := n.aliases[peer]
	if !ok {
		return delivery.ErrUnknownPeer
	}
	dialog, err := n.core.Connect(ctx, alias)
	if err != nil {
		return err
	}
	// Queue identity is always the key, even if a local alias was reused later.
	if dialog.Peer().PublicKey != peer {
		return delivery.ErrUnknownPeer
	}
	return dialog.Send(ctx, message)
}
func (n deliveryNetwork) Acknowledge(ctx context.Context, peer [32]byte, id protocol.MessageID) error {
	for _, dialog := range n.core.Dialogs() {
		if dialog.Peer().PublicKey == peer {
			return dialog.Acknowledge(ctx, id)
		}
	}
	return chat.ErrDialogClosed
}
func (n deliveryNetwork) Disconnect(peer [32]byte) error {
	for _, dialog := range n.core.Dialogs() {
		if dialog.Peer().PublicKey == peer {
			return dialog.Close()
		}
	}
	return nil
}
