package tor

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
)

// Node is a net.Listener backed by an onion v3 service and its own Tor process.
// Use Dial for outgoing connections. All returned streams still require peer
// authentication (securechannel); onion reachability alone is not trust.
type Node struct {
	process  *process
	listener net.Listener
	address  string
	socks    string
	dialTime time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	closed   bool
	err      error
	closeErr error
	streams  map[*trackedConn]struct{}
}

var _ net.Listener = (*Node)(nil)

// Start returns after bootstrap and at least one successful descriptor upload.
// ctx owns the whole node lifetime: cancellation stops Tor and closes streams.
// StartTimeout limits only setup. A restored profile derives the same address.
func Start(ctx context.Context, keys identity.KeyPair, options Options) (_ *Node, err error) {
	options, err = options.defaults()
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	seed, err := keys.OnionServiceSeed()
	if err != nil {
		return nil, err
	}
	defer clear(seed)
	private := ed25519.NewKeyFromSeed(seed)
	address := onionAddress(private.Public().(ed25519.PublicKey))
	clear(private)
	key := newServiceKey(seed)
	defer clear(key[:])
	startup, cancelStartup := context.WithTimeout(ctx, options.StartTimeout)
	defer cancelStartup()
	reporter := &progressReporter{callback: options.OnProgress}
	phase := StartingProcess
	if options.Connection.Mode == "obfs4" {
		phase = StartingTransport
	}
	reporter.report(Progress{Phase: phase})
	p, err := startProcess(startup, options)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			select {
			case <-p.transportDone():
				err = errors.Join(err, ErrObfs4Exited)
			default:
			}
			err = errors.Join(err, p.close())
		}
	}()
	// A failed PT must interrupt controller startup as well as live operation.
	go func() {
		select {
		case <-p.transportDone():
			cancelStartup()
		case <-startup.Done():
		}
	}()
	reporter.report(Progress{Phase: ConnectingController})
	raw, err := p.connect(startup)
	if err != nil {
		return nil, startupError(startup, "connect controller", err)
	}
	listener, err := (&net.ListenConfig{}).Listen(startup, "tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, startupError(startup, "listen locally", ErrControl)
	}
	defer func() {
		if err != nil {
			_ = listener.Close()
		}
	}()
	socks, err := configure(startup, raw, filepath.Join(p.runDir, "control.cookie"), &key, address, listener.Addr().String(), reporter)
	if err != nil {
		return nil, err
	}
	select {
	case <-startup.Done():
		return nil, startup.Err()
	case <-p.done:
		return nil, ErrProcessExited
	default:
	}
	lifetime, cancel := context.WithCancel(ctx)
	n := &Node{
		process: p, listener: listener, address: address, socks: socks,
		dialTime: options.DialTimeout, ctx: lifetime, cancel: cancel,
		done: make(chan struct{}), streams: make(map[*trackedConn]struct{}),
	}
	go func() {
		select {
		case <-ctx.Done():
			n.shutdown(ctx.Err())
		case <-p.done:
			n.shutdown(ErrProcessExited)
		case <-p.transportDone():
			n.shutdown(ErrObfs4Exited)
		case <-n.done:
		}
	}()
	reporter.report(Progress{Phase: Ready, Percent: 100, Step: BootstrapDone})
	return n, nil
}

func (n *Node) Address() string { return n.address }
func (n *Node) Addr() net.Addr  { return onionAddr(n.address) }

// Done closes once connections, process and owned runtime files are cleaned up.
func (n *Node) Done() <-chan struct{} { return n.done }

// Err reports an unexpected process exit or lifetime cancellation. Explicit
// Close has no terminal cause; cleanup failures are returned by Close instead.
func (n *Node) Err() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.err
}

// Accept blocks until an onion stream reaches the local listener or Close.
func (n *Node) Accept() (net.Conn, error) {
	raw, err := n.listener.Accept()
	if err != nil {
		return nil, err
	}
	return n.track(raw)
}

// Close is concurrent-safe and idempotent. It closes streams, stops pending
// dials and reaps Tor, killing it after ShutdownTimeout if needed.
func (n *Node) Close() error {
	n.shutdown(nil)
	return n.closeErr
}

func (n *Node) shutdown(cause error) {
	n.once.Do(func() {
		n.mu.Lock()
		n.closed, n.err = true, cause
		streams := make([]*trackedConn, 0, len(n.streams))
		for stream := range n.streams {
			streams = append(streams, stream)
		}
		n.mu.Unlock()
		n.cancel()
		_ = n.listener.Close()
		for _, stream := range streams {
			_ = stream.Close()
		}
		n.closeErr = n.process.close()
		close(n.done)
	})
}

func (n *Node) track(raw net.Conn) (net.Conn, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		_ = raw.Close()
		return nil, net.ErrClosed
	}
	conn := &trackedConn{Conn: raw, node: n}
	n.streams[conn] = struct{}{}
	return conn, nil
}

type trackedConn struct {
	net.Conn
	node *Node
	// Each Close still calls the underlying Conn.Close (net.Conn requires
	// concurrent safety); map deletion is safe and naturally idempotent.
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.node.mu.Lock()
	delete(c.node.streams, c)
	c.node.mu.Unlock()
	return err
}
