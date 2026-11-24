package delivery

import (
	"context"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/chat"
)

func (s *Service) dispatch() {
	defer s.workers.Done()
	ticker := time.NewTicker(s.options.PollInterval)
	defer ticker.Stop()
	finished := make(chan [32]byte, s.options.MaxWorkers)
	active := make(map[[32]byte]bool)
	for {
		if s.ctx.Err() != nil {
			return
		}
		if len(active) < s.options.MaxWorkers {
			// Allow for rows whose deadline just elapsed while their worker is
			// finishing, without letting them hide other peers' due messages.
			items, err := s.store.Due(s.ctx, time.Now(), s.options.MaxWorkers*2)
			if err != nil {
				s.stop(err)
				return
			}
			for _, pending := range items {
				if len(active) == s.options.MaxWorkers {
					break
				}
				if active[pending.Peer] {
					continue
				}
				now := time.Now()
				recovery := now.Add(s.options.AttemptTimeout + max(s.options.AckTimeout, s.options.retryDelay(pending.Attempts+1)))
				item, ready, err := s.store.BeginAttempt(s.ctx, pending.Sequence, now, recovery)
				if err != nil {
					s.stop(err)
					return
				}
				if !ready {
					continue
				}
				active[item.Peer] = true
				s.workers.Add(1)
				go func() {
					defer s.workers.Done()
					s.attempt(item)
					select {
					case finished <- item.Peer:
					case <-s.ctx.Done():
					}
				}()
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case peer := <-finished:
			delete(active, peer)
		case <-s.wake:
		case <-ticker.C:
		}
	}
}

func (s *Service) attempt(item Item) {
	ctx, cancel := context.WithTimeout(s.ctx, s.options.AttemptTimeout)
	err := s.network.Send(ctx, item.Peer, chat.Message{ID: item.ID, Text: item.Text})
	cancel()
	if s.ctx.Err() != nil {
		return
	}
	delay := s.options.retryDelay(item.Attempts)
	if err == nil {
		delay = max(delay, s.options.AckTimeout)
	}
	// Both failed and unacknowledged writes remain pending. Never infer delivery
	// from Send's success, or overwrite an ACK that raced with this call.
	if err := s.store.Retry(s.ctx, item.Sequence, item.Attempts, time.Now().Add(delay)); err != nil {
		s.stop(err)
	}
}
