// Package tor owns a Tor client process and exposes onion-only byte streams.
// Streams must be authenticated with securechannel before application use.
package tor

import (
	"errors"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/onion"
)

var (
	ErrInvalidAddress    = onion.ErrInvalidAddress
	ErrInvalidOptions    = errors.New("invalid Tor options")
	ErrUnavailable       = errors.New("Tor executable unavailable")
	ErrDataDirectory     = errors.New("Tor data directory unavailable or unsafe")
	ErrInUse             = errors.New("Tor data directory already in use")
	ErrProcessExited     = errors.New("Tor process exited")
	ErrControl           = errors.New("Tor control operation failed")
	ErrAuthentication    = errors.New("Tor SAFECOOKIE authentication failed")
	ErrDial              = errors.New("Tor onion connection failed")
	ErrInvalidConnection = errors.New("invalid Tor connection settings")
	ErrObfs4Unavailable  = errors.New("obfs4 executable unavailable")
	ErrObfs4Startup      = errors.New("obfs4 transport startup failed")
	ErrObfs4Exited       = errors.New("obfs4 transport exited")
)

type Options struct {
	Connection Connection
	// OnProgress runs serially during Start. It must return promptly and must
	// not wait for Start to finish. Nil disables progress reporting.
	OnProgress func(Progress)
	// BinaryPath defaults to "tor" on PATH. Nothing is downloaded at runtime.
	BinaryPath string
	// DataDir must be dedicated to ShadowTalk's Tor instance. It retains guard
	// state and directory caches across runs. One running node may own it.
	DataDir string
	// StartTimeout includes process startup, bootstrap and descriptor upload.
	// Zero selects 3 minutes. Negative timeouts are rejected.
	StartTimeout time.Duration
	// DialTimeout defaults to 1 minute; ShutdownTimeout defaults to 5 seconds.
	DialTimeout     time.Duration
	ShutdownTimeout time.Duration
}

func (o Options) defaults() (Options, error) {
	if err := o.Connection.Validate(); err != nil {
		return o, err
	}
	o.Connection.Bridges = append([]string(nil), o.Connection.Bridges...)
	if o.DataDir == "" || o.StartTimeout < 0 || o.DialTimeout < 0 || o.ShutdownTimeout < 0 {
		return o, ErrInvalidOptions
	}
	if o.BinaryPath == "" {
		o.BinaryPath = "tor"
	}
	if o.StartTimeout == 0 {
		o.StartTimeout = 3 * time.Minute
	}
	if o.DialTimeout == 0 {
		o.DialTimeout = time.Minute
	}
	if o.ShutdownTimeout == 0 {
		o.ShutdownTimeout = 5 * time.Second
	}
	return o, nil
}
