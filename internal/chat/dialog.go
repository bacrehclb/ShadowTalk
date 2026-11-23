package chat

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
)

type Dialog struct {
	service   *Service
	peer      contacts.Contact
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	workers   sync.WaitGroup
	writeGate chan struct{}
	mu        sync.Mutex
	closed    bool
	err       error
	closeErr  error
	links     map[bool]*link // one incoming and one outgoing at most
	primary   *link
}

func newDialog(service *Service, peer contacts.Contact) *Dialog {
	ctx, cancel := context.WithCancel(service.ctx)
	d := &Dialog{service: service, peer: peer, ctx: ctx, cancel: cancel,
		done: make(chan struct{}), writeGate: make(chan struct{}, 1), links: make(map[bool]*link)}
	d.writeGate <- struct{}{}
	return d
}

func (d *Dialog) Peer() contacts.Contact { return d.peer }
func (d *Dialog) Done() <-chan struct{}  { return d.done }
func (d *Dialog) Err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

// Close aborts in-flight I/O and unqueued receive events. It does not wait for
// UI consumption, send acknowledgements or flush data. Repeated calls are safe.
func (d *Dialog) Close() error {
	d.stop(nil)
	<-d.done
	return d.closeErr
}

func (d *Dialog) stop(cause error) {
	d.mu.Lock()
	d.stopLocked(cause)
	d.mu.Unlock()
}

func (d *Dialog) stopLocked(cause error) {
	if !d.closed {
		d.closed, d.err = true, cause
		d.cancel()
	}
}

func (d *Dialog) run() {
	defer d.service.workers.Done()
	<-d.ctx.Done()
	d.stop(d.ctx.Err())
	d.mu.Lock()
	links := make([]*link, 0, len(d.links))
	for _, link := range d.links {
		links = append(links, link)
	}
	d.mu.Unlock()
	for _, link := range links {
		d.closeErr = errors.Join(d.closeErr, link.close())
	}
	d.workers.Wait()
	d.service.remove(d)
	close(d.done)
}

func (d *Dialog) attach(session Session) (*link, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.ctx.Err() != nil {
		return nil, ErrDialogClosed
	}
	if d.links[session.Incoming()] != nil {
		return nil, nil
	}
	link := newLink(d.ctx, session, d.service.options.FrameTimeout)
	d.links[session.Incoming()] = link
	if d.primary == nil {
		d.primary = link
	}
	d.workers.Add(1)
	return link, nil
}

func (d *Dialog) detach(link *link, cause error) {
	_ = link.close()
	d.mu.Lock()
	if d.links[link.session.Incoming()] == link {
		delete(d.links, link.session.Incoming())
	}
	if d.primary == link {
		d.primary = nil
		for _, other := range d.links {
			if other.ctx.Err() == nil {
				d.primary = other
			}
		}
	}
	if len(d.links) == 0 {
		d.stopLocked(cause)
	}
	d.mu.Unlock()
}

func (d *Dialog) readLoop(link *link) {
	defer d.workers.Done()
	var failure error
	defer func() { d.detach(link, failure) }()
	for {
		frame, err := link.readFrame()
		if err != nil {
			failure = err
			if errors.Is(err, protocol.ErrInvalidFrame) || errors.Is(err, protocol.ErrFrameTooLarge) {
				d.stop(err)
			}
			return
		}
		kind := MessageReceived
		if frame.Type == protocol.TypeAck {
			kind = AcknowledgementReceived
		}
		event := Event{Kind: kind, Peer: d.peer, Message: Message{ID: frame.ID, Text: frame.Text}}
		if link.ctx.Err() != nil {
			failure = link.ctx.Err()
			return
		}
		select {
		case d.service.events <- event:
		case <-link.ctx.Done():
			failure = link.ctx.Err()
			return
		}
	}
}

func normalizeClose(err error) error {
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
