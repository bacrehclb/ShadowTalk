package tor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/securechannel"
)

func fakeOptions(t *testing.T, mode string) Options {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHADOWTALK_FAKE_TOR", mode)
	t.Setenv("SHADOWTALK_FAKE_REGISTRY", t.TempDir())
	// The race detector otherwise sleeps a second in every helper process exit.
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	return Options{BinaryPath: binary, DataDir: filepath.Join(t.TempDir(), "tor"), StartTimeout: 5 * time.Second, DialTimeout: time.Second, ShutdownTimeout: 500 * time.Millisecond}
}

func testKeys(t *testing.T) identity.KeyPair {
	t.Helper()
	keys, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func startFake(t *testing.T, ctx context.Context, keys identity.KeyPair, options Options) *Node {
	t.Helper()
	node, err := Start(ctx, keys, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := node.Close(); err != nil {
			t.Error(err)
		}
	})
	return node
}

func waitDone(t *testing.T, node *Node) {
	t.Helper()
	select {
	case <-node.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("node shutdown did not finish")
	}
}

func assertRuntimeRemoved(t *testing.T, directory string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(directory, ".shadowtalk-run-*"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("runtime files left behind: %v, %v", paths, err)
	}
}

func TestNodeRestartPreservesAddressAndSerializesDataDirectory(t *testing.T) {
	options := fakeOptions(t, "normal")
	keys := testKeys(t)
	node := startFake(t, t.Context(), keys, options)
	address := node.Address()
	if ValidateAddress(address) != nil || node.Addr().Network() != "tor" || node.Addr().String() != address+":443" {
		t.Fatal("invalid listener address")
	}
	if _, err := Start(t.Context(), keys, options); !errors.Is(err, ErrInUse) {
		t.Fatalf("duplicate owner: %v", err)
	}
	// Tor guard/cache files survive; only this run's control files are removed.
	statePath := filepath.Join(options.DataDir, "state")
	if err := os.WriteFile(statePath, []byte("retained state"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := node.Close(); err != nil {
		t.Fatal(err)
	}
	if node.Err() != nil {
		t.Fatalf("normal close: %v", node.Err())
	}
	assertRuntimeRemoved(t, options.DataDir)
	seed, _ := keys.Seed()
	restored, _ := identity.RestoreKeyPair(seed)
	clear(seed)
	restarted := startFake(t, t.Context(), restored, options)
	if restarted.Address() != address {
		t.Fatal("address changed after restoring identity")
	}
	data, err := os.ReadFile(statePath)
	if err != nil || string(data) != "retained state" {
		t.Fatal("persistent Tor state was removed")
	}
}

func TestStartupFailuresAreBoundedAndReleaseResources(t *testing.T) {
	for _, test := range []struct {
		mode string
		want error
	}{
		{"exit", ErrProcessExited}, {"stall-port", context.DeadlineExceeded},
		{"null-auth", ErrAuthentication}, {"foreign-cookie", ErrAuthentication},
		{"bad-auth", ErrAuthentication}, {"stall-auth", context.DeadlineExceeded},
		{"remote-socks", ErrControl}, {"mismatch-onion", ErrControl},
		{"fail-onion", ErrControl}, {"no-publish", context.DeadlineExceeded},
		{"wrong-publication", context.DeadlineExceeded},
	} {
		t.Run(test.mode, func(t *testing.T) {
			options := fakeOptions(t, test.mode)
			options.StartTimeout = 750 * time.Millisecond
			options.ShutdownTimeout = 50 * time.Millisecond
			keys := testKeys(t)
			started := time.Now()
			node, err := Start(t.Context(), keys, options)
			if node != nil || !errors.Is(err, test.want) {
				t.Fatalf("startup: %v, %v", node, err)
			}
			if time.Since(started) > 3*time.Second {
				t.Fatal("startup exceeded its budget")
			}
			if strings.Contains(err.Error(), "SECRET_ECHO") || strings.Contains(err.Error(), "ED25519-V3:") {
				t.Fatal("raw Tor error leaked secret command")
			}
			assertRuntimeRemoved(t, options.DataDir)
			t.Setenv("SHADOWTALK_FAKE_TOR", "normal")
			options.StartTimeout = 5 * time.Second
			_ = startFake(t, t.Context(), keys, options) // Failed startup released ownership.
		})
	}
}

func TestLifetimeCancellationAndUnexpectedExit(t *testing.T) {
	for _, action := range []string{"cancel", "kill", "force-close"} {
		t.Run(action, func(t *testing.T) {
			mode := "normal"
			if action == "force-close" {
				mode = "ignore-close"
			}
			options := fakeOptions(t, mode)
			options.ShutdownTimeout = 50 * time.Millisecond
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			node := startFake(t, ctx, testKeys(t), options)
			accepted := make(chan error, 1)
			go func() { _, err := node.Accept(); accepted <- err }()
			var want error
			switch action {
			case "cancel":
				want = context.Canceled
				cancel()
			case "kill":
				want = ErrProcessExited
				if err := node.process.command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			case "force-close":
				var callers sync.WaitGroup
				for range 8 {
					callers.Go(func() {
						if err := node.Close(); err != nil {
							t.Error(err)
						}
					})
				}
				callers.Wait()
			}
			waitDone(t, node)
			if !errors.Is(node.Err(), want) {
				t.Fatalf("terminal cause: %v", node.Err())
			}
			if err := <-accepted; !errors.Is(err, net.ErrClosed) {
				t.Fatalf("blocked accept: %v", err)
			}
			if _, err := node.Dial(t.Context(), node.Address()); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("dial closed node: %v", err)
			}
			assertRuntimeRemoved(t, options.DataDir)
		})
	}
}

func TestDialCancellationAndNoDirectFallback(t *testing.T) {
	options := fakeOptions(t, "socks-stall")
	node := startFake(t, t.Context(), testKeys(t), options)
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := node.Dial(ctx, node.Address()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel SOCKS negotiation: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("SOCKS cancellation leaked a blocked dial")
	}
	for _, address := range []string{"127.0.0.1", "localhost", "example.com"} {
		if _, err := node.Dial(t.Context(), address); !errors.Is(err, ErrInvalidAddress) {
			t.Fatalf("non-onion dial: %v", err)
		}
	}
	finished := make(chan error, 1)
	go func() { _, err := node.Dial(t.Context(), node.Address()); finished <- err }()
	if err := node.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("dial succeeded after shutdown")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown left a pending dial")
	}
}

func TestTwoNodesCarryPinnedTLSOverSOCKS(t *testing.T) {
	options := fakeOptions(t, "normal")
	aliceKeys, bobKeys := testKeys(t), testKeys(t)
	alice := startFake(t, t.Context(), aliceKeys, options)
	options.DataDir = filepath.Join(t.TempDir(), "tor")
	bob := startFake(t, t.Context(), bobKeys, options)
	aliceTLS, _ := securechannel.New(aliceKeys, securechannel.Options{})
	bobTLS, _ := securechannel.New(bobKeys, securechannel.Options{})
	result := make(chan error, 1)
	go func() {
		raw, err := bob.Accept()
		if err != nil {
			result <- err
			return
		}
		conn, err := bobTLS.Server(t.Context(), raw, aliceKeys.PublicKey())
		if err != nil {
			result <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		message := make([]byte, 5)
		if _, err = io.ReadFull(conn, message); err == nil && !bytes.Equal(message, []byte("hello")) {
			err = errors.New("wrong plaintext")
		}
		if err == nil {
			_, err = conn.Write([]byte("reply"))
		}
		result <- err
	}()
	ctx, cancel := context.WithCancel(t.Context())
	raw, err := alice.Dial(ctx, bob.Address())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel() // Successful streams outlive their dial context.
	conn, err := aliceTLS.Client(t.Context(), raw, bobKeys.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err = conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 5)
	if _, err = io.ReadFull(conn, reply); err != nil || string(reply) != "reply" {
		t.Fatalf("reply: %q, %v", reply, err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := alice.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("after shutdown")); err == nil {
		t.Fatal("node shutdown left stream open")
	}
}

func TestStartRejectsInvalidConfigurationBeforeCreatingState(t *testing.T) {
	options := fakeOptions(t, "normal")
	keys := testKeys(t)
	bad := options
	bad.DataDir = ""
	if _, err := Start(t.Context(), keys, bad); !errors.Is(err, ErrInvalidOptions) {
		t.Fatal(err)
	}
	bad = options
	bad.DialTimeout = -time.Second
	if _, err := Start(t.Context(), keys, bad); !errors.Is(err, ErrInvalidOptions) {
		t.Fatal(err)
	}
	bad = options
	bad.BinaryPath = filepath.Join(t.TempDir(), "missing-tor")
	if _, err := Start(t.Context(), keys, bad); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Start(ctx, keys, options); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := Start(t.Context(), identity.KeyPair{}, options); !errors.Is(err, identity.ErrInvalidKeyPair) {
		t.Fatal(err)
	}
	if _, err := os.Stat(options.DataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid configuration created runtime state")
	}
	if runtime.GOOS != "windows" {
		if err := os.Mkdir(options.DataDir, 0755); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(options.DataDir, 0755)
		if _, err := Start(t.Context(), keys, options); !errors.Is(err, ErrDataDirectory) {
			t.Fatalf("accepted shared directory: %v", err)
		}
	}
}
