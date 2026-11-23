package peers

import (
	"errors"
	"net"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/securechannel"
)

// Session is an authenticated byte stream with a copy of its confirmed contact.
// Close aborts the underlying stream without waiting for a TLS close-notify or
// flushing application data. EOF and non-timeout I/O failures also release it.
// Idle disconnects are discovered on I/O; there is no background reader.
type Session struct {
	connection *securechannel.Conn
	slot       *connectionSlot
	peer       contacts.Contact
	incoming   bool
}

var _ net.Conn = (*Session)(nil)

func (s *Session) Peer() contacts.Contact             { return s.peer }
func (s *Session) Incoming() bool                     { return s.incoming }
func (s *Session) Done() <-chan struct{}              { return s.slot.done }
func (s *Session) Close() error                       { return s.slot.close() }
func (s *Session) LocalAddr() net.Addr                { return s.connection.LocalAddr() }
func (s *Session) RemoteAddr() net.Addr               { return s.connection.RemoteAddr() }
func (s *Session) SetDeadline(t time.Time) error      { return s.connection.SetDeadline(t) }
func (s *Session) SetReadDeadline(t time.Time) error  { return s.connection.SetReadDeadline(t) }
func (s *Session) SetWriteDeadline(t time.Time) error { return s.connection.SetWriteDeadline(t) }

func (s *Session) Read(data []byte) (int, error) {
	select {
	case <-s.Done():
		return 0, net.ErrClosed
	default:
	}
	n, err := s.connection.Read(data)
	s.releaseOnFailure(err)
	return n, err
}

func (s *Session) Write(data []byte) (int, error) {
	select {
	case <-s.Done():
		return 0, net.ErrClosed
	default:
	}
	n, err := s.connection.Write(data)
	s.releaseOnFailure(err)
	return n, err
}

func (s *Session) releaseOnFailure(err error) {
	if err == nil {
		return
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return
	}
	_ = s.Close()
}

// A slot exists before a dial starts and until the raw connection is closed.
// Its mutable fields are guarded by manager.mu; closeErr is read after done.
type connectionSlot struct {
	manager  *Manager
	raw      net.Conn
	closed   bool
	done     chan struct{}
	closeErr error
}

func (s *connectionSlot) close() error {
	m := s.manager
	m.mu.Lock()
	if s.closed {
		m.mu.Unlock()
		<-s.done
		return s.closeErr
	}
	s.closed = true
	raw := s.raw
	m.mu.Unlock()
	var err error
	if raw != nil {
		err = raw.Close()
		if errors.Is(err, net.ErrClosed) {
			err = nil
		}
	}
	m.mu.Lock()
	delete(m.entries, s)
	s.closeErr = err
	close(s.done)
	m.mu.Unlock()
	return err
}
