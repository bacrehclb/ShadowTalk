package tor

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func runFakePT(mode string) error {
	if os.Getenv("TOR_PT_CLIENT_TRANSPORTS") != "obfs4" || os.Getenv("TOR_PT_MANAGED_TRANSPORT_VER") != "1" || os.Getenv("TOR_PT_EXIT_ON_STDIN_CLOSE") != "1" || os.Getenv("TOR_PT_PROXY") != "" {
		return fmt.Errorf("unexpected PT environment")
	}
	if mode == "pt-stall" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		return nil
	}
	if mode == "pt-invalid" {
		fmt.Println("VERSION 1\nCMETHOD obfs4 socks5 192.0.2.1:9999\nCMETHODS DONE")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return nil
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	fmt.Printf("VERSION 1\nCMETHOD obfs4 socks5 %s\nCMETHODS DONE\n", listener.Addr())
	_, _ = io.Copy(io.Discard, os.Stdin)
	if mode == "pt-ignore-close" {
		time.Sleep(time.Minute)
	}
	return nil
}

func TestTransportHandshakeRejectsUntrustedEndpoints(t *testing.T) {
	good := "VERSION 1\nLOG SEVERITY=notice MESSAGE=ignored\nCMETHOD obfs4 socks5 127.0.0.1:1234\nCMETHODS DONE\n"
	if endpoint, err := transportHandshake(bufio.NewReader(strings.NewReader(good))); err != nil || endpoint != "127.0.0.1:1234" {
		t.Fatal(endpoint, err)
	}
	for _, text := range []string{
		strings.Replace(good, "127.0.0.1", "example.com", 1), strings.Replace(good, "127.0.0.1", "0.0.0.0", 1),
		strings.Replace(good, "socks5", "socks4", 1), strings.Replace(good, "VERSION 1\n", "", 1),
		"VERSION 2\n", "VERSION 1\nCMETHODS DONE\n", "VERSION 1\nCMETHOD-ERROR PRIVATE\n", strings.Repeat("X", 5000) + "\n",
	} {
		if _, err := transportHandshake(bufio.NewReader(strings.NewReader(text))); err != ErrObfs4Startup {
			t.Fatal("invalid IPC accepted", err)
		}
	}
}

func TestTransportEnvironmentDoesNotInheritProxyOrServerMode(t *testing.T) {
	env := transportEnvironment([]string{"PATH=kept", "TOR_PT_PROXY=private", "tor_pt_server_transports=obfs4", "TOR_PT_STATE_LOCATION=other"}, "our-state")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "private") || strings.Contains(joined, "server_transports") || strings.Contains(joined, "other") || !strings.Contains(joined, "PATH=kept") || !strings.Contains(joined, "TOR_PT_STATE_LOCATION=our-state") {
		t.Fatal("inherited PT settings")
	}
}
