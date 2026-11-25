package tor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPortFileReadRetryPolicy(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"success", nil, false},
		{"missing", os.ErrNotExist, true},
		{"wrapped-missing", &os.PathError{Op: "open", Path: "control.port", Err: os.ErrNotExist}, true},
		{"sharing-violation", syscall.Errno(32), runtime.GOOS == "windows"},
		{"wrapped-sharing-violation", &os.PathError{Op: "open", Path: "control.port", Err: syscall.Errno(32)}, runtime.GOOS == "windows"},
		{"access-denied", syscall.Errno(5), false},
		{"lock-violation", syscall.Errno(33), false},
		{"permission", os.ErrPermission, false},
		{"invalid-control-file", ErrControl, false},
		{"unknown", errors.New("unknown read failure"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := canRetryPortFileRead(test.err); got != test.want {
				t.Fatalf("can retry = %v, want %v", got, test.want)
			}
		})
	}
}

func TestConnectRejectsInvalidControlPortFiles(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
	}{
		{"empty", ""},
		{"partial", "PORT="},
		{"remote", "PORT=192.0.2.1:9051\n"},
		{"invalid-port", "PORT=127.0.0.1:0\n"},
		{"oversized", strings.Repeat("x", 257)},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &process{runDir: t.TempDir()}
			if err := os.WriteFile(filepath.Join(p.runDir, "control.port"), []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			conn, err := p.connect(ctx)
			if conn != nil {
				_ = conn.Close()
				t.Fatal("connected using an invalid port file")
			}
			if !errors.Is(err, ErrControl) {
				t.Fatalf("expected immediate control error, got %v", err)
			}
		})
	}
}
