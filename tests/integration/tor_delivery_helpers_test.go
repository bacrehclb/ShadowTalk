package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/app"
	"github.com/bacrehclb/ShadowTalk/internal/chat"
	"github.com/bacrehclb/ShadowTalk/internal/contacts"
	"github.com/bacrehclb/ShadowTalk/internal/delivery"
	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/identity"
	"github.com/bacrehclb/ShadowTalk/internal/peers"
	"github.com/bacrehclb/ShadowTalk/internal/storage/contactsfile"
	"github.com/bacrehclb/ShadowTalk/internal/storage/profiledir"
	"github.com/bacrehclb/ShadowTalk/internal/storage/sqlite"
	"github.com/bacrehclb/ShadowTalk/internal/transport/tor"
)

type torDeliverySettings struct {
	binary            string
	startup, exchange time.Duration
	delivery          delivery.Options
	connection        tor.Connection
	report            func(string, tor.Progress)
}

type torDeliveryPeer struct {
	dir     string
	keys    *torTestKeys
	profile identity.Profile
	address string
	service *delivery.Service
}

func newTorDeliveryPeer(t *testing.T, nickname string) *torDeliveryPeer {
	t.Helper()
	p := &torDeliveryPeer{dir: filepath.Join(t.TempDir(), nickname), keys: &torTestKeys{values: make(map[string][]byte)}}
	loaded, err := app.NewProfiles(profiledir.New(p.dir), p.keys).Create(nickname)
	if err != nil {
		t.Fatal(err)
	}
	p.profile = loaded.Profile
	p.address, err = loaded.Keys.OnionAddress()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.close(); err != nil {
			t.Error(err)
		}
		p.keys.clear()
	})
	return p
}

func (p *torDeliveryPeer) addContact(t *testing.T, other *torDeliveryPeer) {
	t.Helper()
	err := contactsfile.New(p.dir, p.profile.PublicKey).Update(func(book *contacts.Book) error {
		_, err := book.Add(contacts.Contact{
			Alias: other.profile.Nickname, Nickname: other.profile.Nickname,
			PublicKey: other.profile.PublicKey, Onion: other.address, AddedAt: time.Now(),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Drain every startup result, even on failure, so no late Tor process or SQLite
// lock outlives the test. Pair cancellation also stops the other pending startup.
func startTorDeliveryPair(t *testing.T, ctx context.Context, settings torDeliverySettings, pair ...*torDeliveryPeer) {
	t.Helper()
	lifetime, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	type result struct {
		peer    *torDeliveryPeer
		service *delivery.Service
		err     error
	}
	results := make(chan result, len(pair))
	for _, peer := range pair {
		go func() {
			service, err := peer.start(lifetime, settings)
			results <- result{peer, service, err}
		}()
	}
	var failures error
	for range pair {
		r := <-results
		r.peer.service = r.service
		if r.err != nil {
			failures = errors.Join(failures, fmt.Errorf("%s startup: %w", r.peer.profile.Nickname, r.err))
			cancel()
		}
	}
	if failures != nil {
		t.Fatal(failures)
	}
}

func (p *torDeliveryPeer) start(ctx context.Context, settings torDeliverySettings) (*delivery.Service, error) {
	profiles := app.NewProfiles(profiledir.New(p.dir), p.keys)
	sessions := app.NewPeerSessions(profiles, contactsfile.New(p.dir, p.profile.PublicKey), func(lifetime context.Context, keys identity.KeyPair) (peers.Transport, error) {
		address, err := keys.OnionAddress()
		if err != nil || address != p.address {
			return nil, errors.New("onion identity changed on reload")
		}
		return tor.Start(lifetime, keys, tor.Options{BinaryPath: settings.binary, DataDir: filepath.Join(p.dir, "tor"), StartTimeout: settings.startup, Connection: settings.connection, OnProgress: func(progress tor.Progress) {
			if settings.report != nil {
				settings.report(p.profile.Nickname, progress)
			}
		}})
	})
	return sessions.StartDelivery(ctx, func(ctx context.Context, owner [32]byte, key []byte) (delivery.Store, error) {
		return sqlite.Open(ctx, p.dir, owner, key)
	}, peers.Options{HandshakeTimeout: time.Minute}, chat.Options{WriteTimeout: time.Minute, FrameTimeout: time.Minute}, settings.delivery)
}

func (p *torDeliveryPeer) close() error {
	var err error
	if p.service != nil {
		err = p.service.Close()
		p.service = nil
	}
	paths, globErr := filepath.Glob(filepath.Join(p.dir, "tor", ".shadowtalk-run-*"))
	if len(paths) != 0 {
		err = errors.Join(err, errors.New("Tor runtime files remain after shutdown"))
	}
	return errors.Join(err, globErr)
}

func (p *torDeliveryPeer) inspect(t *testing.T, ctx context.Context, check func(delivery.Store)) {
	t.Helper()
	loaded, err := app.NewProfiles(profiledir.New(p.dir), p.keys).Load()
	if err != nil {
		t.Fatal(err)
	}
	key, err := loaded.Keys.HistoryKey()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	store, err := sqlite.Open(ctx, p.dir, p.profile.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	check(store)
}

func waitTorDelivery(t *testing.T, ctx context.Context, timeout time.Duration, from, to *torDeliveryPeer, message chat.Message, stage string, ready func(delivery.Item) bool) delivery.Item {
	t.Helper()
	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		item, err := from.service.Status(wait, to.profile.PublicKey, message.ID)
		if err != nil {
			t.Fatalf("%s %s status: %v (service: %v)", from.profile.Nickname, stage, err, from.service.Err())
		}
		if ready(item) {
			return item
		}
		select {
		case <-tick.C:
		case <-from.service.Done():
			t.Fatalf("%s %s stopped: %v", from.profile.Nickname, stage, from.service.Err())
		case <-wait.Done():
			t.Fatalf("%s %s: %v (state=%d, attempts=%d)", from.profile.Nickname, stage, wait.Err(), item.State, item.Attempts)
		}
	}
}

type torHistoryMessage struct {
	direction history.Direction
	message   chat.Message
}

func assertTorHistory(t *testing.T, ctx context.Context, reader interface {
	List(context.Context, [32]byte, history.Query) (history.Page, error)
}, peer [32]byte, want []torHistoryMessage) {
	t.Helper()
	page, err := reader.List(ctx, peer, history.Query{})
	if err != nil || len(page.Entries) != len(want) {
		t.Fatalf("history: got %d entries, want %d: %v", len(page.Entries), len(want), err)
	}
	// List is newest first; the scenario records its expectations oldest first.
	for i, entry := range page.Entries {
		expected := want[len(want)-1-i]
		if entry.Peer != peer || entry.Direction != expected.direction || entry.ID != expected.message.ID || entry.Text != expected.message.Text {
			t.Fatalf("history entry %d has unexpected identity, direction or content", i)
		}
	}
}

func assertTorDelivered(t *testing.T, ctx context.Context, store delivery.Store, peer [32]byte, messages ...chat.Message) {
	t.Helper()
	for _, message := range messages {
		item, err := store.Status(ctx, peer, message.ID)
		if err != nil || item.State != delivery.Delivered || item.DeliveredAt.IsZero() {
			t.Fatalf("delivered status did not persist: %v", err)
		}
	}
}

// Test-only credential store: encrypted seeds still use the normal disk format.
// Reloading services retains these synthetic wrapping keys, never OS secrets.
type torTestKeys struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (k *torTestKeys) Get(id string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	key, ok := k.values[id]
	if !ok {
		return nil, identity.ErrEncryptionKeyNotFound
	}
	return bytes.Clone(key), nil
}
func (k *torTestKeys) Set(id string, key []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.values[id] = bytes.Clone(key)
	return nil
}
func (k *torTestKeys) Delete(id string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	clear(k.values[id])
	delete(k.values, id)
	return nil
}
func (k *torTestKeys) clear() {
	k.mu.Lock()
	defer k.mu.Unlock()
	for id, key := range k.values {
		clear(key)
		delete(k.values, id)
	}
}
