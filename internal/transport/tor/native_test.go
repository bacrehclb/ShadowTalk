package tor

import (
	"context"
	"crypto/ed25519"
	"net/textproto"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cretz/bine/control"
)

// A supplied real Tor binary validates its CLI, SAFECOOKIE and key encoding
// without enabling network access. Ordinary tests do not require Tor installed.
func TestNativeTorControl(t *testing.T) {
	binary := os.Getenv("SHADOWTALK_TOR_BINARY")
	if binary == "" {
		t.Skip("set SHADOWTALK_TOR_BINARY to test an installed Tor executable")
	}
	options, _ := (Options{BinaryPath: binary, DataDir: filepath.Join(t.TempDir(), "tor")}).defaults()
	p, err := startProcess(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := p.close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	raw, err := p.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deadline, _ := ctx.Deadline()
	_ = raw.SetDeadline(deadline)
	c := control.NewConn(textproto.NewConn(raw))
	if err := authenticate(c, filepath.Join(p.runDir, "control.cookie")); err != nil {
		t.Fatal(err)
	}
	if err := c.TakeOwnership(); err != nil {
		t.Fatal("TAKEOWNERSHIP failed")
	}
	seed, _ := testKeys(t).OnionServiceSeed()
	defer clear(seed)
	key := newServiceKey(seed)
	defer clear(key[:])
	private := ed25519.NewKeyFromSeed(seed)
	defer clear(private)
	expected := onionAddress(private.Public().(ed25519.PublicKey))
	service, err := c.AddOnion(&control.AddOnionRequest{Key: &key, Flags: []string{"DiscardPK"}, Ports: []*control.KeyVal{{Key: virtualPort, Val: "127.0.0.1:1"}}})
	if err != nil {
		t.Fatal("real Tor rejected ADD_ONION")
	}
	if service.ServiceID+".onion" != expected || service.Key != nil {
		t.Fatal("real Tor returned an unexpected identity or private key")
	}
}

// Starts real Lyrebird and validates Tor's actual interpretation of our config,
// while DisableNetwork=1 prevents bridge/public-network connections.
func TestNativeObfs4Control(t *testing.T) {
	binary, plugin := os.Getenv("SHADOWTALK_TOR_BINARY"), os.Getenv("SHADOWTALK_LYREBIRD_BINARY")
	if binary == "" || plugin == "" {
		t.Skip("set SHADOWTALK_TOR_BINARY and SHADOWTALK_LYREBIRD_BINARY")
	}
	options, err := (Options{BinaryPath: binary, DataDir: filepath.Join(t.TempDir(), "tor"), Connection: Connection{Mode: "obfs4", LyrebirdBinary: plugin, Bridges: []string{testBridge()}}}).defaults()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	p, err := startProcess(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := p.close(); err != nil {
			t.Error(err)
		}
	}()
	raw, err := p.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deadline, _ := ctx.Deadline()
	_ = raw.SetDeadline(deadline)
	c := control.NewConn(textproto.NewConn(raw))
	if err := authenticate(c, filepath.Join(p.runDir, "control.cookie")); err != nil {
		t.Fatal(err)
	}
	if err := c.TakeOwnership(); err != nil {
		t.Fatal("TAKEOWNERSHIP failed")
	}
	values, err := c.GetConf("UseBridges", "DisableNetwork", "Bridge", "ClientTransportPlugin")
	if err != nil {
		t.Fatal("GETCONF failed")
	}
	want := map[string]string{"UseBridges": "1", "DisableNetwork": "1", "Bridge": testBridge(), "ClientTransportPlugin": "obfs4 socks5 " + p.transport.endpoint}
	for _, v := range values {
		if want[v.Key] != v.Val {
			t.Fatal("Tor interpreted the connection config differently")
		}
		delete(want, v.Key)
	}
	if len(want) != 0 {
		t.Fatal("Tor did not confirm all connection settings")
	}
}
