package tor

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func testBridge() string {
	return "obfs4 127.0.0.1:1 " + strings.Repeat("A", 40) + " cert=" + base64.RawStdEncoding.EncodeToString(make([]byte, 52)) + " iat-mode=0"
}

func TestConnectionRejectsInjectionAndAmbiguousModes(t *testing.T) {
	valid := Connection{Mode: "obfs4", LyrebirdBinary: "/path with spaces/lyrebird", Bridges: []string{testBridge()}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Connection{
		{Mode: "unknown"}, {Mode: "direct", Bridges: valid.Bridges}, {Mode: "direct", LyrebirdBinary: "lyrebird"},
		{Mode: "obfs4", LyrebirdBinary: "lyrebird"}, {Mode: "obfs4", Bridges: valid.Bridges},
		{Mode: "obfs4", LyrebirdBinary: "bad\npath", Bridges: valid.Bridges},
	} {
		if !errors.Is(c.Validate(), ErrInvalidConnection) {
			t.Fatal("invalid connection accepted")
		}
	}
	for _, line := range []string{
		testBridge() + "\nSocksPort 0", testBridge() + " extra=value", strings.Replace(testBridge(), "obfs4", "snowflake", 1),
		strings.Replace(testBridge(), "127.0.0.1:1", "bridge.example:443", 1),
		strings.Replace(testBridge(), "127.0.0.1:1", "127.0.0.1:0", 1),
		strings.Replace(testBridge(), "cert=", "cert=broken", 1), strings.Replace(testBridge(), "iat-mode=0", "iat-mode=3", 1),
	} {
		if validBridge(line) {
			t.Fatal("invalid bridge accepted")
		}
	}
	if !validBridge(strings.Replace(testBridge(), "127.0.0.1:1", "[2001:db8::1]:443", 1)) {
		t.Fatal("IPv6 bridge rejected")
	}
}

func TestManagedObfs4UsesPrivateConfigAndStopsWithNode(t *testing.T) {
	options := fakeOptions(t, "normal")
	// Same test executable impersonates the PT when Tor's command-line options
	// are absent. Exercise the exact path, including spaces and Unicode.
	alias := filepath.Join(t.TempDir(), "плагин with spaces"+filepath.Ext(options.BinaryPath))
	data, err := os.ReadFile(options.BinaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alias, data, 0700); err != nil {
		t.Fatal(err)
	}
	options.Connection = Connection{Mode: "obfs4", LyrebirdBinary: alias, Bridges: []string{testBridge()}}
	var stages []Progress
	options.OnProgress = func(p Progress) { stages = append(stages, p) }
	node := startFake(t, t.Context(), testKeys(t), options)
	pt := node.process.transport
	if pt == nil || !loopbackEndpoint(pt.endpoint) {
		t.Fatal("missing managed transport")
	}
	if !slices.ContainsFunc(stages, func(p Progress) bool { return p.Phase == StartingTransport }) || stages[len(stages)-1].Phase != Ready {
		t.Fatal("startup progress missing")
	}
	config, err := os.ReadFile(filepath.Join(node.process.runDir, "torrc"))
	if err != nil || !strings.Contains(string(config), "UseBridges 1\n") || !strings.Contains(string(config), "ClientTransportPlugin obfs4 socks5 "+pt.endpoint) {
		t.Fatal("bridge config missing", err)
	}
	if strings.Contains(strings.Join(node.process.command.Args, " "), "cert=") {
		t.Fatal("bridge leaked into argv")
	}
	if err := node.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pt.done:
	default:
		t.Fatal("transport remained alive")
	}
	assertRuntimeRemoved(t, options.DataDir)
}

func TestObfs4FailureAndCancellationReleaseOwnership(t *testing.T) {
	for _, mode := range []string{"pt-invalid", "pt-stall", "pt-ignore-close", "pt-kill"} {
		t.Run(mode, func(t *testing.T) {
			options := fakeOptions(t, mode)
			options.Connection = Connection{Mode: "obfs4", LyrebirdBinary: options.BinaryPath, Bridges: []string{testBridge()}}
			options.ShutdownTimeout = 50 * time.Millisecond
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			if mode == "pt-ignore-close" || mode == "pt-kill" {
				node := startFake(t, t.Context(), testKeys(t), options)
				if mode == "pt-kill" {
					if err := node.process.transport.command.Process.Kill(); err != nil {
						t.Fatal(err)
					}
					waitDone(t, node)
					if !errors.Is(node.Err(), ErrObfs4Exited) {
						t.Fatal(node.Err())
					}
				} else {
					started := time.Now()
					if err := node.Close(); err != nil {
						t.Fatal(err)
					}
					if time.Since(started) > 3*time.Second {
						t.Fatal("PT ignored bounded shutdown")
					}
				}
			} else {
				_, err := Start(ctx, testKeys(t), options)
				if mode == "pt-stall" && !errors.Is(err, context.DeadlineExceeded) || mode == "pt-invalid" && !errors.Is(err, ErrObfs4Startup) {
					t.Fatal(err)
				}
			}
			assertRuntimeRemoved(t, options.DataDir)
			t.Setenv("SHADOWTALK_FAKE_TOR", "normal")
			node := startFake(t, t.Context(), testKeys(t), options)
			if err := node.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBootstrapProgressSanitizesWarningsAndTimesOutAtLastStage(t *testing.T) {
	options := fakeOptions(t, "bootstrap-warning")
	options.StartTimeout = 600 * time.Millisecond
	var reports []Progress
	options.OnProgress = func(p Progress) { reports = append(reports, p) }
	_, err := Start(t.Context(), testKeys(t), options)
	var failure *BootstrapError
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &failure) {
		t.Fatal(err)
	}
	if failure.Progress.Percent != 14 || failure.Progress.Step != BootstrapHandshake || failure.Progress.Problem != BootstrapTimeout {
		t.Fatal(failure.Progress)
	}
	if strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), "100%") {
		t.Fatal("raw warning leaked")
	}
	count := 0
	for _, p := range reports {
		if p.Phase == Bootstrapping && p.Percent == 14 {
			count++
		}
	}
	if count != 1 {
		t.Fatal("duplicate progress notifications", count)
	}
	assertRuntimeRemoved(t, options.DataDir)
}

func TestBootstrapParserHandlesQuotedFields(t *testing.T) {
	text := `WARN BOOTSTRAP PROGRESS=14 TAG=handshake SUMMARY="PRIVATE PROGRESS=100 REASON=PT_MISSING" REASON=TIMEOUT`
	p, ok := parseBootstrap(text)
	if !ok || p.Percent != 14 || p.Problem != BootstrapTimeout {
		t.Fatal(p, ok)
	}
	for _, raw := range []string{text + " PROGRESS=100", text + ` X="unfinished`, "NOTICE BOOTSTRAP PROGRESS=101 TAG=done", "garbage"} {
		if _, ok := parseBootstrap(raw); ok {
			t.Fatal("bad bootstrap accepted")
		}
	}
	p, ok = parseBootstrap("NOTICE BOOTSTRAP PROGRESS=42 TAG=future_stage")
	if !ok || p.Percent != 42 || p.Step != BootstrapStarting {
		t.Fatal("future stage rejected")
	}
}
