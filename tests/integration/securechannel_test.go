package integration

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/securechannel"
)

// Only public connection information crosses the process boundary outside TLS.
// The child node generates its own identity and never exports its seed.
type nodeAddress struct {
	Address   string
	PublicKey string
}

func TestSecureChannelServerProcess(t *testing.T) {
	if os.Getenv("SHADOWTALK_CHANNEL_TEST") != "1" {
		t.Skip("child process helper")
	}
	if err := serveTestNode(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func serveTestNode() error {
	peer, err := hex.DecodeString(os.Getenv("SHADOWTALK_CHANNEL_TEST_PEER"))
	if err != nil {
		return err
	}
	keys, err := identity.GenerateKeyPair()
	if err != nil {
		return err
	}
	endpoint, err := securechannel.New(keys, securechannel.Options{})
	if err != nil {
		return err
	}
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(nodeAddress{listener.Addr().String(), hex.EncodeToString(keys.PublicKey())}); err != nil {
		return err
	}
	raw, err := listener.Accept()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := endpoint.Server(ctx, raw, peer)
	if err != nil {
		return err
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	message := make([]byte, len("hello from another process"))
	if _, err := io.ReadFull(connection, message); err != nil {
		return err
	}
	if string(message) != "hello from another process" {
		return fmt.Errorf("unexpected message")
	}
	_, err = connection.Write([]byte("authenticated reply"))
	return err
}

func TestSecureChannelBetweenIndependentProcesses(t *testing.T) {
	keys, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := securechannel.New(keys, securechannel.Options{})
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-test.run=^TestSecureChannelServerProcess$")
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), "SHADOWTALK_CHANNEL_TEST=1", "SHADOWTALK_CHANNEL_TEST_PEER="+hex.EncodeToString(keys.PublicKey()))
	var diagnostic bytes.Buffer
	command.Stderr = &diagnostic
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	var peer nodeAddress
	if err := json.NewDecoder(io.LimitReader(output, 4096)).Decode(&peer); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		waited = true
		t.Fatalf("server startup: %v; %s", err, diagnostic.String())
	}
	publicKey, err := hex.DecodeString(peer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp4", peer.Address)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := endpoint.Client(ctx, raw, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if !bytes.Equal(connection.PeerPublicKey(), publicKey) {
		t.Fatal("wrong server identity")
	}
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte("hello from another process")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, len("authenticated reply"))
	if _, err := io.ReadFull(connection, reply); err != nil || string(reply) != "authenticated reply" {
		t.Fatalf("reply: %q, %v", reply, err)
	}
	// Read close_notify so the server can finish a graceful TLS shutdown.
	if _, err := connection.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("server close: %v", err)
	}
	err = command.Wait()
	waited = true
	if err != nil || diagnostic.Len() != 0 {
		t.Fatalf("server exit: %v; %s", err, diagnostic.String())
	}
}
