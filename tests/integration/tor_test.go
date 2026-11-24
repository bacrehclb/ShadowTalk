package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/delivery"
	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/storage/torconfig"
	"github.com/bacrehclb/ShadowTalk/internal/transport/tor"
)

// The same scenario runs in CI through a protocol-compatible local subprocess.
// Only the Tor executable and time budgets differ from the opt-in live test.
func TestTorDeliveryRestart(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "tor-helper")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	buildCtx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(buildCtx, "go", "test", "-c", "-o", binary, "./internal/transport/tor")
	cmd.Dir = filepath.Join("..", "..")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build Tor fixture: %v\n%s", err, output)
	}
	t.Setenv("SHADOWTALK_FAKE_TOR", "normal")
	t.Setenv("SHADOWTALK_FAKE_REGISTRY", t.TempDir())
	ctx, stop := context.WithTimeout(t.Context(), time.Minute)
	defer stop()
	runTorDelivery(t, ctx, torDeliverySettings{
		binary: binary, startup: 10 * time.Second, exchange: 10 * time.Second,
		delivery: delivery.Options{
			PollInterval: 10 * time.Millisecond, RetryMin: 50 * time.Millisecond, RetryMax: 100 * time.Millisecond,
			AttemptTimeout: 2 * time.Second, AckTimeout: 100 * time.Millisecond,
		},
	})
}

// Explicit opt-in: two Tor processes publish services to the public network.
// Profiles and messages are synthetic; the real OS credential store is unused.
// Retain the original test name for existing scripts invoking the live check.
func TestTorSecureChannelLive(t *testing.T) {
	if os.Getenv("SHADOWTALK_TOR_LIVE_TEST") != "1" {
		t.Skip("set SHADOWTALK_TOR_LIVE_TEST=1 and optionally SHADOWTALK_TOR_BINARY")
	}
	if os.Getenv("SHADOWTALK_FAKE_TOR") != "" {
		t.Fatal("unset SHADOWTALK_FAKE_TOR before running the live check")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	settings := torDeliverySettings{
		binary: os.Getenv("SHADOWTALK_TOR_BINARY"), startup: 3 * time.Minute, exchange: 3 * time.Minute,
		// Use the application's normal retry and recovery deadlines on real Tor.
		delivery: delivery.Options{},
		report: func(nickname string, p tor.Progress) {
			t.Logf("%s: Tor phase=%d bootstrap=%d%% step=%d problem=%d", nickname, p.Phase, p.Percent, p.Step, p.Problem)
		},
	}
	if path := os.Getenv("SHADOWTALK_TOR_CONFIG"); path != "" {
		config, err := torconfig.Read(path)
		if err != nil {
			t.Fatal(err)
		}
		settings.connection, settings.startup = config.Connection, config.StartTimeout
		if settings.binary == "" {
			settings.binary = config.BinaryPath
		}
	}
	runTorDelivery(t, ctx, settings)
}

func runTorDelivery(t *testing.T, ctx context.Context, settings torDeliverySettings) {
	t.Helper()
	alice, bob := newTorDeliveryPeer(t, "alice"), newTorDeliveryPeer(t, "bob")
	alice.addContact(t, bob)
	bob.addContact(t, alice)
	startTorDeliveryPair(t, ctx, settings, alice, bob)
	t.Log("both nodes bootstrapped and published onion descriptors")

	queue := func(from, to *torDeliveryPeer, text string) chat.Message {
		t.Helper()
		message, err := chat.NewMessage(text)
		if err != nil {
			t.Fatal(err)
		}
		item, added, err := from.service.Queue(ctx, to.profile.PublicKey, message)
		if err != nil || !added || item.ID != message.ID || item.State != delivery.Pending {
			t.Fatalf("%s queue: added=%v, error=%v", from.profile.Nickname, added, err)
		}
		return message
	}
	ack := func(from, to *torDeliveryPeer, message chat.Message) delivery.Item {
		t.Helper()
		return waitTorDelivery(t, ctx, settings.exchange, from, to, message, "delivery ACK", func(item delivery.Item) bool {
			return item.State == delivery.Delivered && item.Attempts > 0 && !item.DeliveredAt.IsZero()
		})
	}
	hello := queue(alice, bob, "Привет через Tor 👋")
	ack(alice, bob, hello)
	wantAlice := []torHistoryMessage{{history.Outgoing, hello}}
	wantBob := []torHistoryMessage{{history.Incoming, hello}}
	assertTorHistory(t, ctx, bob.service, alice.profile.PublicKey, wantBob)

	reply := queue(bob, alice, "authenticated Tor reply")
	ack(bob, alice, reply)
	wantAlice = append(wantAlice, torHistoryMessage{history.Incoming, reply})
	wantBob = append(wantBob, torHistoryMessage{history.Outgoing, reply})
	assertTorHistory(t, ctx, alice.service, bob.profile.PublicKey, wantAlice)
	t.Log("bidirectional messages saved and acknowledged over pinned TLS")

	// Close the receiver before enqueueing: the next message cannot be ACKed.
	if err := bob.close(); err != nil {
		t.Fatal(err)
	}
	offline := queue(alice, bob, "queued offline; survives restarting both peers")
	waitTorDelivery(t, ctx, settings.exchange, alice, bob, offline, "offline attempt", func(item delivery.Item) bool {
		return item.State == delivery.Pending && item.Attempts > 0
	})
	if err := alice.close(); err != nil {
		t.Fatal(err)
	}
	wantAlice = append(wantAlice, torHistoryMessage{history.Outgoing, offline})
	var attempts int64
	alice.inspect(t, ctx, func(store delivery.Store) {
		assertTorHistory(t, ctx, store, bob.profile.PublicKey, wantAlice)
		item, err := store.Status(ctx, bob.profile.PublicKey, offline.ID)
		if err != nil || item.State != delivery.Pending || item.Attempts == 0 || item.NextAttempt.IsZero() {
			t.Fatalf("pending queue did not persist: %v", err)
		}
		attempts = item.Attempts
	})
	bob.inspect(t, ctx, func(store delivery.Store) {
		assertTorHistory(t, ctx, store, alice.profile.PublicKey, wantBob)
	})
	t.Log("offline message remains pending on disk; both nodes stopped cleanly")

	// Reload the saved seeds, contacts and SQLite files; never re-enqueue offline.
	startTorDeliveryPair(t, ctx, settings, alice, bob)
	t.Log("both nodes restarted; waiting for the persistent queue to resume")
	if resumed := ack(alice, bob, offline); resumed.Attempts <= attempts {
		t.Fatal("queue did not make a new attempt after restart")
	}
	wantBob = append(wantBob, torHistoryMessage{history.Incoming, offline})
	assertTorHistory(t, ctx, bob.service, alice.profile.PublicKey, wantBob)
	for _, peer := range []*torDeliveryPeer{alice, bob} {
		if err := peer.close(); err != nil {
			t.Fatal(err)
		}
	}
	alice.inspect(t, ctx, func(store delivery.Store) {
		assertTorHistory(t, ctx, store, bob.profile.PublicKey, wantAlice)
		assertTorDelivered(t, ctx, store, bob.profile.PublicKey, hello, offline)
	})
	bob.inspect(t, ctx, func(store delivery.Store) {
		assertTorHistory(t, ctx, store, alice.profile.PublicKey, wantBob)
		assertTorDelivered(t, ctx, store, alice.profile.PublicKey, reply)
	})
	t.Log("restart recovery passed: exact message IDs, no duplicate history, durable ACK status, released stores and Tor runtime files")
}
