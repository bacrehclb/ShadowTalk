package tor

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ShadowTalk owns the PT process directly: Tor's ClientTransportPlugin exec
// syntax splits paths on whitespace. Managed-transport IPC instead lets us use
// exec.Command with the exact executable path on every platform.
type obfs4Process struct {
	command       *exec.Cmd
	stdin         io.WriteCloser
	stdout        io.ReadCloser
	done, drained chan struct{}
	endpoint      string
	timeout       time.Duration
}

func startObfs4(ctx context.Context, binary, state string, timeout time.Duration) (_ *obfs4Process, err error) {
	path, err := exec.LookPath(binary)
	if err != nil {
		return nil, ErrObfs4Unavailable
	}
	if err := os.Mkdir(state, 0700); err != nil {
		return nil, ErrDataDirectory
	}
	p := &obfs4Process{command: exec.Command(path), done: make(chan struct{}), drained: make(chan struct{}), timeout: timeout}
	p.command.Env = transportEnvironment(os.Environ(), state)
	p.command.Stderr = io.Discard
	p.stdin, err = p.command.StdinPipe()
	if err != nil {
		return nil, ErrObfs4Startup
	}
	p.stdout, err = p.command.StdoutPipe()
	if err != nil {
		_ = p.stdin.Close()
		return nil, ErrObfs4Startup
	}
	if err := p.command.Start(); err != nil {
		_ = p.stdin.Close()
		_ = p.stdout.Close()
		return nil, ErrObfs4Unavailable
	}
	go func() { _ = p.command.Wait(); close(p.done) }()
	defer func() {
		if err != nil {
			p.close()
		}
	}()
	type result struct {
		endpoint string
		err      error
	}
	ready := make(chan result, 1)
	go func() {
		defer close(p.drained)
		reader := bufio.NewReader(p.stdout)
		endpoint, err := transportHandshake(reader)
		ready <- result{endpoint, err}
		// Logs are never retained or echoed, including after startup.
		_, _ = io.Copy(io.Discard, reader)
	}()
	select {
	case r := <-ready:
		if r.err != nil {
			return nil, r.err
		}
		p.endpoint = r.endpoint
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.done:
		return nil, ErrObfs4Exited
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	select {
	case <-p.done:
		return nil, ErrObfs4Exited
	default:
		return p, nil
	}
}

func transportEnvironment(env []string, state string) []string {
	result := make([]string, 0, len(env)+4)
	for _, value := range env {
		name, _, _ := strings.Cut(value, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "TOR_PT_") {
			result = append(result, value)
		}
	}
	return append(result, "TOR_PT_MANAGED_TRANSPORT_VER=1", "TOR_PT_CLIENT_TRANSPORTS=obfs4", "TOR_PT_EXIT_ON_STDIN_CLOSE=1", "TOR_PT_STATE_LOCATION="+state)
}

func transportHandshake(reader *bufio.Reader) (string, error) {
	version, endpoint := false, ""
	for range 64 {
		line, prefix, err := reader.ReadLine()
		if err != nil || prefix {
			return "", ErrObfs4Startup
		}
		fields := strings.Fields(string(line))
		if len(fields) == 0 {
			return "", ErrObfs4Startup
		}
		switch fields[0] {
		case "VERSION":
			if version || len(fields) != 2 || fields[1] != "1" {
				return "", ErrObfs4Startup
			}
			version = true
		case "CMETHOD":
			if !version || endpoint != "" || len(fields) != 4 || fields[1] != "obfs4" || fields[2] != "socks5" || !loopbackEndpoint(fields[3]) {
				return "", ErrObfs4Startup
			}
			endpoint = fields[3]
		case "CMETHODS":
			if !version || endpoint == "" || len(fields) != 2 || fields[1] != "DONE" {
				return "", ErrObfs4Startup
			}
			return endpoint, nil
		case "LOG", "STATUS": // bounded and ignored; may contain private data
		default:
			return "", ErrObfs4Startup
		}
	}
	return "", ErrObfs4Startup
}

// Called once by the owner, after Tor has stopped using its SOCKS listener.
func (p *obfs4Process) close() {
	_ = p.stdin.Close() // TOR_PT_EXIT_ON_STDIN_CLOSE is the portable graceful exit.
	timer := time.NewTimer(p.timeout)
	defer timer.Stop()
	select {
	case <-p.done:
	case <-timer.C:
		_ = p.command.Process.Kill()
		<-p.done
	}
	_ = p.stdout.Close()
	<-p.drained
}
