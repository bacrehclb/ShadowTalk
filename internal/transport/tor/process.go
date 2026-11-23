package tor

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// process owns resources from preparation through reap. Only Start or Node's
// shutdown path calls close; the waiter only records process termination.
type process struct {
	command *exec.Cmd
	done    chan struct{}
	lock    *flock.Flock
	runDir  string
	control net.Conn
	timeout time.Duration
}

func startProcess(options Options) (_ *process, err error) {
	binary, err := exec.LookPath(options.BinaryPath)
	if err != nil {
		return nil, ErrUnavailable
	}
	directory, err := filepath.Abs(options.DataDir)
	if err != nil || os.MkdirAll(directory, 0700) != nil {
		return nil, ErrDataDirectory
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return nil, ErrDataDirectory
	}
	lockPath := filepath.Join(directory, ".shadowtalk.lock")
	if info, err = os.Lstat(lockPath); err == nil && !info.Mode().IsRegular() || err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, ErrDataDirectory
	}
	p := &process{lock: flock.New(lockPath), timeout: options.ShutdownTimeout}
	locked, err := p.lock.TryLock()
	if err != nil {
		return nil, ErrDataDirectory
	}
	if !locked {
		return nil, ErrInUse
	}
	defer func() {
		if err != nil {
			_ = p.close()
		}
	}()
	p.runDir, err = os.MkdirTemp(directory, ".shadowtalk-run-")
	if err != nil {
		return nil, ErrDataDirectory
	}
	torrc := filepath.Join(p.runDir, "torrc")
	if err = os.WriteFile(torrc, nil, 0600); err != nil {
		return nil, ErrDataDirectory
	}
	p.command = exec.Command(binary, torArguments(directory, p.runDir)...)
	// Tor/control messages can contain addresses and key material. Raw logs are
	// deliberately not forwarded to the terminal or included in returned errors.
	p.command.Stdout, p.command.Stderr = io.Discard, io.Discard
	if err = p.command.Start(); err != nil {
		p.command = nil
		return nil, ErrUnavailable
	}
	p.done = make(chan struct{})
	go func() {
		_ = p.command.Wait()
		close(p.done)
	}()
	return p, nil
}

func torArguments(directory, runDir string) []string {
	return []string{
		"-f", filepath.Join(runDir, "torrc"),
		"--DataDirectory", directory,
		"--ControlPort", "127.0.0.1:auto",
		"--ControlPortWriteToFile", filepath.Join(runDir, "control.port"),
		"--CookieAuthentication", "1",
		"--CookieAuthFile", filepath.Join(runDir, "control.cookie"),
		"--SocksPort", "127.0.0.1:auto OnionTrafficOnly IsolateSOCKSAuth",
		"--ClientOnly", "1", "--DisableNetwork", "1", "--SafeSocks", "1",
		"--AvoidDiskWrites", "1", "--Log", "err stdout",
		"--__OwningControllerProcess", strconv.Itoa(os.Getpid()),
	}
}

func (p *process) connect(ctx context.Context) (net.Conn, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := readSmallFile(filepath.Join(p.runDir, "control.port"), 256)
		if err == nil {
			address, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "PORT=")
			if !ok || !loopbackEndpoint(address) {
				return nil, ErrControl
			}
			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp4", address)
			if err == nil {
				p.control = conn
				return conn, nil
			}
		} else if !canRetryPortFileRead(err) {
			return nil, ErrControl
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.done:
			return nil, ErrProcessExited
		case <-ticker.C:
		}
	}
}

func readSmallFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrControl
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if len(data) > int(limit) {
		return nil, ErrControl
	}
	return data, err
}

func loopbackEndpoint(address string) bool {
	host, port, err := net.SplitHostPort(address)
	number, numberErr := strconv.Atoi(port)
	return err == nil && numberErr == nil && host == "127.0.0.1" && number > 0 && number <= 65535
}

func (p *process) close() error {
	if p.control != nil {
		_ = p.control.Close() // TAKEOWNERSHIP makes Tor exit on controller loss.
	}
	if p.done != nil {
		timer := time.NewTimer(p.timeout)
		select {
		case <-p.done:
		case <-timer.C:
			_ = p.command.Process.Kill()
			<-p.done // Reap before releasing the data directory lock.
		}
		timer.Stop()
	}
	var cleanup error
	if p.runDir != "" {
		cleanup = os.RemoveAll(p.runDir)
	}
	if p.lock != nil {
		cleanup = errors.Join(cleanup, p.lock.Close())
	}
	if cleanup != nil {
		return ErrDataDirectory
	}
	return nil
}
