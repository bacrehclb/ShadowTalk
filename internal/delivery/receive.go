package delivery

import (
	"context"
	"errors"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/history"
)

func (s *Service) receive() {
	defer s.workers.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case event, ok := <-s.network.Events():
			if !ok {
				if s.ctx.Err() == nil {
					s.stop(errors.Join(ErrNetwork, s.network.Err()))
				}
				return
			}
			if !s.network.HasPeer(event.Peer.PublicKey) {
				s.stop(ErrUnknownPeer)
				return
			}
			switch event.Kind {
			case chat.MessageReceived:
				_, added, err := s.store.Append(s.ctx, history.Message{Peer: event.Peer.PublicKey, Direction: history.Incoming, ID: event.Message.ID, Text: event.Message.Text, RecordedAt: time.Now()})
				if errors.Is(err, history.ErrConflict) {
					// A peer reused its ID with a different payload. Preserve the
					// original, never ACK the conflict, and end only its dialog.
					_ = s.network.Disconnect(event.Peer.PublicKey)
					continue
				}
				if err != nil {
					s.stop(err)
					return
				}
				if added {
					s.changed()
				}
				ctx, cancel := context.WithTimeout(s.ctx, s.options.AckWriteTimeout)
				_ = s.network.Acknowledge(ctx, event.Peer.PublicKey, event.Message.ID)
				cancel() // A lost ACK is repaired by the sender's same-ID retry.
			case chat.AcknowledgementReceived:
				if event.Message.Text != "" {
					s.stop(ErrNetwork)
					return
				}
				changed, err := s.store.Acknowledge(s.ctx, event.Peer.PublicKey, event.Message.ID, time.Now())
				if err != nil {
					s.stop(err)
					return
				}
				if changed {
					s.changed()
					s.trigger()
				}
			default:
				s.stop(ErrNetwork)
				return
			}
		}
	}
}
