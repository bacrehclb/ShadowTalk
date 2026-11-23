package peers

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net"
	"sync"

	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/securechannel"
)

// Transport must honor Dial's context and unblock Accept when closed. Close
// must be safe alongside in-flight calls. tor.Node implements this interface.
type Transport interface {
	Accept() (net.Conn, error)
	Dial(context.Context, string) (net.Conn, error)
	Address() string
	Close() error
	Err() error
}

// OpenTransport starts a transport whose lifetime follows ctx. On success the
// manager takes ownership. On failure the factory must clean up its resources,
// as tor.Start does; the returned transport is ignored.
type OpenTransport func(ctx context.Context) (Transport, error)

type Manager struct {
	transport Transport
	endpoint  *securechannel.Endpoint
	options   Options
	address   string
	byAlias   map[string]contacts.Contact
	byKey     map[[32]byte]contacts.Contact
	peerKeys  []ed25519.PublicKey
	ctx       context.Context
	cancel    context.CancelFunc
	incoming  chan *Session
	done      chan struct{}
	stopOnce  sync.Once
	workers   sync.WaitGroup

	mu         sync.Mutex
	closed     bool
	err        error
	closeErr   error
	entries    map[*connectionSlot]struct{}
	handshakes int
}

// Start validates configuration before opening the transport. It takes a copy
// of the confirmed contact book; changing the file or Book afterward does not
// change trust in this running manager. Restart to apply contact changes.
// ctx owns the entire manager lifetime, including all returned sessions.
func Start(ctx context.Context, keys identity.KeyPair, book *contacts.Book, options Options, open OpenTransport) (*Manager, error) {
	if ctx == nil || book == nil || open == nil {
		return nil, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	options, err := options.defaults()
	if err != nil {
		return nil, err
	}
	endpoint, err := securechannel.New(keys, securechannel.Options{HandshakeTimeout: options.HandshakeTimeout})
	if err != nil {
		return nil, err
	}
	if book.Owner() != [32]byte(keys.PublicKey()) {
		return nil, ErrOwnerMismatch
	}
	snapshot, err := contacts.RestoreBook(book.Owner(), book.List())
	if err != nil {
		return nil, err
	}
	address, err := keys.OnionAddress()
	if err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	transport, err := open(lifetime)
	if err != nil {
		cancel()
		return nil, err
	}
	err = lifetime.Err()
	if err == nil && transport == nil {
		err = ErrInvalidConfig
	}
	if err == nil && transport.Address() != address {
		err = ErrTransportIdentity
	}
	if err != nil {
		cancel()
		if transport != nil {
			err = errors.Join(err, transport.Close())
		}
		return nil, err
	}
	m := &Manager{
		transport: transport, endpoint: endpoint, options: options, address: address,
		byAlias: make(map[string]contacts.Contact), byKey: make(map[[32]byte]contacts.Contact),
		ctx: lifetime, cancel: cancel, incoming: make(chan *Session, options.MaxConnections),
		done: make(chan struct{}), entries: make(map[*connectionSlot]struct{}),
	}
	for _, contact := range snapshot.List() {
		m.byAlias[contact.Alias] = contact
		m.byKey[contact.PublicKey] = contact
		m.peerKeys = append(m.peerKeys, ed25519.PublicKey(contact.PublicKey[:]))
	}
	m.workers.Add(1)
	go m.acceptLoop()
	go func() {
		<-lifetime.Done()
		m.stop(lifetime.Err())
	}()
	return m, nil
}

func (m *Manager) Address() string       { return m.address }
func (m *Manager) Done() <-chan struct{} { return m.done }

// Err reports why the manager stopped. An explicit Close has no terminal cause.
func (m *Manager) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

// Close cancels setup, closes queued and delivered sessions, stops the transport
// and waits for workers. It is safe to call concurrently and more than once.
func (m *Manager) Close() error {
	m.stop(nil)
	<-m.done
	return m.closeErr
}

func (m *Manager) stop(cause error) {
	m.stopOnce.Do(func() {
		m.mu.Lock()
		m.closed, m.err = true, cause
		entries := make([]*connectionSlot, 0, len(m.entries))
		for entry := range m.entries {
			entries = append(entries, entry)
		}
		m.mu.Unlock()
		m.cancel()
		// Workers can initiate shutdown too; never make a worker wait for itself.
		go func() {
			var closeErr error
			for _, entry := range entries {
				closeErr = errors.Join(closeErr, entry.close())
			}
			closeErr = errors.Join(closeErr, m.transport.Close())
			m.workers.Wait()
			for {
				select {
				case <-m.incoming:
				default:
					m.closeErr = closeErr
					close(m.done)
					return
				}
			}
		}()
	})
}

// reserve is the only place that adds workers after startup. The closed check
// and Add share the shutdown mutex, so Wait cannot race with a new worker.
func (m *Manager) reserve(raw net.Conn, incoming bool) (*connectionSlot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.Join(net.ErrClosed, m.err)
	}
	if len(m.entries) >= m.options.MaxConnections || incoming && m.handshakes >= m.options.MaxIncomingHandshakes {
		return nil, ErrConnectionLimit
	}
	entry := &connectionSlot{manager: m, raw: raw, done: make(chan struct{})}
	m.entries[entry] = struct{}{}
	if incoming {
		m.handshakes++
	}
	m.workers.Add(1)
	return entry, nil
}
