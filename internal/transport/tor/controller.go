package tor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"path/filepath"
	"strings"
	"time"

	"github.com/cretz/bine/control"
)

func configure(ctx context.Context, raw net.Conn, cookieFile string, key *serviceKey, address, target string, reporter *progressReporter) (string, error) {
	deadline, _ := ctx.Deadline()
	if err := raw.SetDeadline(deadline); err != nil {
		return "", ErrControl
	}
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	c := control.NewConn(textproto.NewConn(raw))
	if err := authenticate(c, cookieFile); err != nil {
		return "", startupError(ctx, "authenticate", err)
	}
	if _, err := c.SendRequest("TAKEOWNERSHIP"); err != nil {
		return "", startupError(ctx, "take ownership", ErrControl)
	}
	// Consume events while synchronous commands pump the control connection.
	// Keep the consumer alive until the last command returns, even on cancel:
	// bine delivers events synchronously and must never block on an abandoned ch.
	events := make(chan control.Event)
	consumerDone := make(chan struct{})
	consumerStopped := make(chan struct{})
	published := make(chan struct{}, 1)
	go func() {
		defer close(consumerStopped)
		for {
			select {
			case <-consumerDone:
				return
			case event := <-events:
				hs, ok := event.(*control.HSDescEvent)
				if ok && hs.Action == "UPLOADED" && hs.Address == strings.TrimSuffix(address, ".onion") {
					select {
					case published <- struct{}{}:
					default:
					}
				}
			}
		}
	}()
	defer func() { close(consumerDone); <-consumerStopped }()
	if err := c.AddEventListener(events, control.EventCodeHSDesc); err != nil {
		return "", startupError(ctx, "subscribe publication", ErrControl)
	}
	service, err := c.AddOnion(&control.AddOnionRequest{
		Key: key, Flags: []string{"DiscardPK"},
		Ports: []*control.KeyVal{{Key: virtualPort, Val: target}},
	})
	if err != nil || service.ServiceID+".onion" != address {
		return "", startupError(ctx, "register onion service", ErrControl)
	}
	if _, err = c.SendRequest("SETCONF DisableNetwork=0"); err != nil {
		return "", startupError(ctx, "enable network", ErrControl)
	}
	// DisableNetwork=1 prevents Tor from opening its SOCKS listener. Query only
	// after SETCONF has enabled networking and bound the ephemeral port.
	socks, err := socksAddress(c)
	if err != nil {
		return "", startupError(ctx, "SOCKS listener", err)
	}
	if err = waitPublished(ctx, c, published, reporter); err != nil {
		return "", err
	}
	if err = c.RemoveEventListener(events, control.EventCodeHSDesc); err != nil {
		return "", startupError(ctx, "unsubscribe publication", ErrControl)
	}
	if !stop() || ctx.Err() != nil {
		return "", startupError(ctx, "finish startup", ErrControl)
	}
	if err = raw.SetDeadline(time.Time{}); err != nil {
		return "", ErrControl
	}
	return socks, nil
}

func authenticate(c *control.Conn, cookieFile string) error {
	info, err := c.ProtocolInfo()
	if err != nil || !info.HasAuthMethod("SAFECOOKIE") || info.HasAuthMethod("NULL") ||
		filepath.Clean(info.CookieFile) != filepath.Clean(cookieFile) {
		return ErrAuthentication
	}
	cookie, err := readSmallFile(cookieFile, 32)
	defer clear(cookie)
	if err != nil || len(cookie) != 32 {
		return ErrAuthentication
	}
	if err = c.Authenticate(""); err != nil {
		return ErrAuthentication
	}
	return nil
}

func socksAddress(c *control.Conn) (string, error) {
	info, err := c.GetInfo("net/listeners/socks")
	if err != nil || len(info) != 1 || info[0].Key != "net/listeners/socks" {
		return "", ErrControl
	}
	// bine already unquotes the single listener value. Reject multiple listeners.
	address := info[0].Val
	if !loopbackEndpoint(address) {
		return "", ErrControl
	}
	return address, nil
}

func waitPublished(ctx context.Context, c *control.Conn, published <-chan struct{}, reporter *progressReporter) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	bootstrapped, uploaded := false, false
	progress := Progress{Phase: Bootstrapping}
	reporter.report(progress)
	failure := func(cause error) error {
		return &BootstrapError{Progress: progress, Cause: errors.Join(cause, contextError(ctx))}
	}
	for {
		info, err := c.GetInfo("status/bootstrap-phase")
		if err != nil {
			return failure(ErrControl)
		}
		for _, item := range info {
			if item.Key == "status/bootstrap-phase" {
				if parsed, ok := parseBootstrap(item.Val); ok {
					progress = parsed
					bootstrapped = progress.Percent == 100
					if bootstrapped {
						progress.Phase = PublishingService
					}
					reporter.report(progress)
				}
			}
		}
		select {
		case <-published:
			uploaded = true
		default:
		}
		if bootstrapped && uploaded {
			return nil
		}
		select {
		case <-ctx.Done():
			return failure(ctx.Err())
		case <-ticker.C:
		}
	}
}

// Never return raw Tor replies: they may echo the ADD_ONION private key.
func startupError(ctx context.Context, stage string, reason error) error {
	if cause := contextError(ctx); cause != nil && !errors.Is(reason, cause) {
		reason = errors.Join(reason, cause)
	}
	return fmt.Errorf("Tor %s: %w", stage, reason)
}

func contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The socket's deadline can fire just before the context timer is scheduled.
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}
