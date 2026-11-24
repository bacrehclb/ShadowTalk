package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/delivery"
	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
)

func outgoing(id byte) history.Message {
	m := testMessage(id)
	m.Direction = history.Outgoing
	return m
}
func enqueueTest(t *testing.T, s *Store, m history.Message) delivery.Item {
	t.Helper()
	item, added, err := s.Enqueue(t.Context(), m)
	if err != nil || !added {
		t.Fatalf("enqueue: added=%v err=%v", added, err)
	}
	return item
}

func TestDeliveryStateMatchesPeerAndSurvivesRestart(t *testing.T) {
	dir := privateTestDir(t)
	s := openTestStore(t, dir)
	m := outgoing(1)
	item := enqueueTest(t, s, m)
	now := m.RecordedAt.Add(time.Minute)
	if got, added, err := s.Enqueue(t.Context(), m); err != nil || added || got != item {
		t.Fatal("enqueue retry changed the record", err)
	}
	if changed, err := s.Acknowledge(t.Context(), m.Peer, m.ID, now); err != nil || changed {
		t.Fatal("unsent message was acknowledged", err)
	}
	item, ready, err := s.BeginAttempt(t.Context(), item.Sequence, now, now.Add(time.Minute))
	if err != nil || !ready || item.Attempts != 1 {
		t.Fatal("begin attempt", err)
	}
	for _, test := range []struct {
		peer [32]byte
		id   protocol.MessageID
	}{{[32]byte{3}, m.ID}, {m.Peer, protocol.MessageID{99}}} {
		if changed, err := s.Acknowledge(t.Context(), test.peer, test.id, now); err != nil || changed {
			t.Fatal("unrelated ACK accepted", err)
		}
	}
	if changed, err := s.Acknowledge(t.Context(), m.Peer, m.ID, now); err != nil || !changed {
		t.Fatal("expected ACK ignored", err)
	}
	if err := s.Retry(t.Context(), item.Sequence, 1, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.Acknowledge(t.Context(), m.Peer, m.ID, now.Add(time.Hour)); err != nil || changed {
		t.Fatal("duplicate ACK changed state", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, dir)
	got, err := s.Status(t.Context(), m.Peer, m.ID)
	if err != nil || got.State != delivery.Delivered || !got.DeliveredAt.Equal(now.Truncate(time.Millisecond)) || got.Attempts != 1 {
		t.Fatal("delivered state lost", err)
	}
	if _, added, err := s.Enqueue(t.Context(), m); err != nil || added {
		t.Fatal("delivered message requeued", err)
	}
	due, err := s.Due(t.Context(), now.Add(time.Hour), 10)
	if err != nil || len(due) != 0 {
		t.Fatal("ACKed message still due", err)
	}
	m.Text = "different"
	if _, _, err := s.Enqueue(t.Context(), m); !errors.Is(err, history.ErrConflict) {
		t.Fatal("conflicting queue entry accepted", err)
	}
}

func TestDeliveryDuePreservesPeerOrderAndPersistsRecoveryDeadline(t *testing.T) {
	dir := privateTestDir(t)
	s := openTestStore(t, dir)
	first := enqueueTest(t, s, outgoing(1))
	enqueueTest(t, s, outgoing(2))
	m := outgoing(3)
	m.Peer = [32]byte{3}
	third := enqueueTest(t, s, m)
	now := first.RecordedAt.Add(time.Minute)
	items, err := s.Due(t.Context(), now, 10)
	if err != nil || len(items) != 2 || items[0].Sequence != first.Sequence || items[1].Sequence != third.Sequence {
		t.Fatal("queue did not select one head per peer", err)
	}
	if _, ready, err := s.BeginAttempt(t.Context(), first.Sequence, now, now.Add(time.Hour)); err != nil || !ready {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, dir)
	items, err = s.Due(t.Context(), now.Add(time.Minute), 10)
	if err != nil || len(items) != 1 || items[0].Sequence != third.Sequence {
		t.Fatal("restart lost lease or bypassed peer head", err)
	}
	if changed, err := s.Acknowledge(t.Context(), first.Peer, first.ID, now); err != nil || !changed {
		t.Fatal(err)
	}
	items, err = s.Due(t.Context(), now.Add(time.Minute), 10)
	if err != nil || len(items) != 2 || items[0].ID[0] != 2 {
		t.Fatal("ACK did not release next message", err)
	}
}

func TestDeliveryQueueAndHistoryCommitTogether(t *testing.T) {
	s := openTestStore(t, privateTestDir(t))
	if _, err := s.db.Exec("CREATE TEMP TRIGGER reject_queue BEFORE INSERT ON outbox BEGIN SELECT RAISE(ABORT, 'sensitive-test-payload'); END"); err != nil {
		t.Fatal(err)
	}
	m := outgoing(1)
	if _, added, err := s.Enqueue(t.Context(), m); err == nil || added || strings.Contains(err.Error(), "sensitive-test-payload") {
		t.Fatal("failed enqueue leaked data or succeeded", err)
	}
	if _, err := s.Get(t.Context(), m.Peer, m.Direction, m.ID); !errors.Is(err, history.ErrNotFound) {
		t.Fatal("history committed without queue", err)
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_queue"); err != nil {
		t.Fatal(err)
	}
	enqueueTest(t, s, m)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := s.Enqueue(ctx, outgoing(2)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDeliveryConcurrentEnqueueAndAckCannotResurrectRows(t *testing.T) {
	s := openTestStore(t, privateTestDir(t))
	m := outgoing(1)
	var wg sync.WaitGroup
	var added atomic.Int32
	for range 20 {
		wg.Go(func() {
			_, ok, err := s.Enqueue(t.Context(), m)
			if err != nil {
				t.Error(err)
			}
			if ok {
				added.Add(1)
			}
		})
	}
	wg.Wait()
	if added.Load() != 1 {
		t.Fatal("duplicate queue rows", added.Load())
	}
	item, err := s.Status(t.Context(), m.Peer, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := m.RecordedAt.Add(time.Minute)
	if _, _, err := s.BeginAttempt(t.Context(), item.Sequence, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var acked atomic.Int32
	for range 20 {
		wg.Go(func() {
			ok, err := s.Acknowledge(t.Context(), m.Peer, m.ID, now)
			if err != nil {
				t.Error(err)
			}
			if ok {
				acked.Add(1)
			}
		})
		wg.Go(func() {
			if err := s.Retry(t.Context(), item.Sequence, 1, now.Add(time.Minute)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	item, err = s.Status(t.Context(), m.Peer, m.ID)
	if err != nil || item.State != delivery.Delivered || acked.Load() != 1 {
		t.Fatal("ACK raced incorrectly with retry", err)
	}
}

func createV1History(t *testing.T, dir string) history.Entry {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, fileName), nil, 0600); err != nil {
		t.Fatal(err)
	}
	db := rawDatabase(t, dir)
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	s := &Store{db: db, owner: testOwner}
	if err := s.create(t.Context(), tx, testRoot()); err != nil {
		t.Fatal(err)
	}
	m := outgoing(1)
	m.RecordedAt = m.RecordedAt.Truncate(time.Millisecond).UTC()
	entry, _, err := s.appendRecord(t.Context(), tx, m)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestDeliveryMigrationPreservesV1HistoryWithoutSendingIt(t *testing.T) {
	dir := privateTestDir(t)
	want := createV1History(t, dir)
	bad := testRoot()
	bad[0] ^= 1
	if _, err := Open(t.Context(), dir, testOwner, bad); !errors.Is(err, history.ErrDecryption) {
		t.Fatal(err)
	}
	db := rawDatabase(t, dir)
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
		t.Fatal("failed open migrated the database", err)
	}
	db.Close()
	s := openTestStore(t, dir)
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatal("migration version", err)
	}
	got, err := s.Get(t.Context(), want.Peer, want.Direction, want.ID)
	if err != nil || got != want {
		t.Fatal("migration changed old ciphertext or history", err)
	}
	items, err := s.Due(t.Context(), time.Now().Add(24*time.Hour), 10)
	if err != nil || len(items) != 0 {
		t.Fatal("migration queued old outgoing history", err)
	}
	if _, err := s.Status(t.Context(), want.Peer, want.ID); !errors.Is(err, history.ErrNotFound) {
		t.Fatal(err)
	}
	enqueueTest(t, s, want.Message) // Only an explicit enqueue may send this record.
}
