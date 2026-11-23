package chat

import (
	"context"
	"sync"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/protocol"
)

type link struct {
	session  Session
	ctx      context.Context
	cancel   context.CancelFunc
	once     sync.Once
	closeErr error
	reader   *protocol.Reader
	writer   *protocol.Writer
	input    frameInput
}

func newLink(parent context.Context, session Session, timeout time.Duration) *link {
	ctx, cancel := context.WithCancel(parent)
	l := &link{session: session, ctx: ctx, cancel: cancel, writer: protocol.NewWriter(session),
		input: frameInput{session: session, timeout: timeout}}
	l.reader = protocol.NewReader(&l.input)
	return l
}

func (l *link) close() error {
	l.once.Do(func() {
		l.cancel()
		l.closeErr = normalizeClose(l.session.Close())
	})
	return l.closeErr
}

func (l *link) readFrame() (protocol.Frame, error) {
	l.input.started = false
	frame, err := l.reader.ReadFrame()
	if err == nil {
		err = l.session.SetReadDeadline(time.Time{})
	}
	if err != nil {
		return protocol.Frame{}, err
	}
	return frame, nil
}

// The first byte may wait indefinitely for an idle conversation. Once a frame
// starts, one fixed deadline covers its remaining header and body; a peer
// cannot keep extending it by dripping bytes. Only the read loop accesses this.
type frameInput struct {
	session Session
	timeout time.Duration
	started bool
}

func (r *frameInput) Read(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if r.started {
		return r.session.Read(data)
	}
	n, err := r.session.Read(data[:1])
	if n != 0 {
		r.started = true
		if deadlineErr := r.session.SetReadDeadline(time.Now().Add(r.timeout)); deadlineErr != nil {
			// Do not report a consumed byte as a completed header when setting
			// its deadline failed. The stream is terminal on this error.
			return 0, deadlineErr
		}
	}
	return n, err
}
