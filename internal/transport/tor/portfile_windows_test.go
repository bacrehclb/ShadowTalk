package tor

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Deny sharing through a real Windows file handle, rather than relying on the
// short scheduling window inside Rename to reproduce ERROR_SHARING_VIOLATION.
func holdPortFileExclusive(t *testing.T, path string) func() {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	release := sync.OnceFunc(func() {
		if err := syscall.CloseHandle(handle); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(release)
	if _, err := readSmallFile(path, 256); !errors.Is(err, errorSharingViolation) {
		t.Fatalf("expected a native sharing violation, got %v", err)
	}
	return release
}

func TestConnectWaitsForWindowsPortFileSharing(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	p := &process{runDir: t.TempDir()}
	path := filepath.Join(p.runDir, "control.port")
	if err := publishFakeControlPort(path, listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	release := holdPortFileExclusive(t, path)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type result struct {
		conn net.Conn
		err  error
	}
	finished := make(chan result, 1)
	go func() {
		conn, err := p.connect(ctx)
		finished <- result{conn, err}
	}()
	select {
	case got := <-finished:
		if got.conn != nil {
			_ = got.conn.Close()
		}
		t.Fatalf("connect returned while the port file was still held: %v", got.err)
	case <-time.After(100 * time.Millisecond):
		// The port file remains unavailable across multiple 25 ms polls.
	}
	release()
	got := <-finished // connect is bounded by ctx even if the retry regresses.
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.conn == nil {
		t.Fatal("connect returned no connection")
	}
	_ = got.conn.Close()
}

func TestConnectWhileWindowsPortFileIsHeldRemainsBounded(t *testing.T) {
	for _, mode := range []string{"deadline", "canceled", "process-exited"} {
		t.Run(mode, func(t *testing.T) {
			p := &process{runDir: t.TempDir(), done: make(chan struct{})}
			path := filepath.Join(p.runDir, "control.port")
			if err := os.WriteFile(path, []byte("PORT=127.0.0.1:9051\n"), 0600); err != nil {
				t.Fatal(err)
			}
			holdPortFileExclusive(t, path)
			budget := 5 * time.Second
			if mode == "deadline" {
				budget = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), budget)
			defer cancel()
			want := error(context.DeadlineExceeded)
			switch mode {
			case "canceled":
				cancel()
				want = context.Canceled
			case "process-exited":
				close(p.done)
				want = ErrProcessExited
			}
			conn, err := p.connect(ctx)
			if conn != nil {
				_ = conn.Close()
				t.Fatal("connected while the port file was held")
			}
			if !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
		})
	}
}
