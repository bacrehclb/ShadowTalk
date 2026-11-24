package sqlite

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
)

var testOwner = [32]byte{1}
var testPeer = [32]byte{2}

func testRoot() []byte { return bytes.Repeat([]byte{0x9a}, 32) }

func privateTestDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "profile")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func openTestStore(t *testing.T, directory string) *Store {
	t.Helper()
	s, err := Open(t.Context(), directory, testOwner, testRoot())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func testMessage(id byte) history.Message {
	return history.Message{Peer: testPeer, Direction: history.Incoming, ID: protocol.MessageID{id}, Text: "Hello, мир!\nSecond line\x1b[31m", RecordedAt: time.Date(2026, 11, 24, 12, 0, 0, 123456789, time.FixedZone("test", 3*60*60))}
}

func appendTest(t *testing.T, s *Store, m history.Message) history.Entry {
	t.Helper()
	entry, inserted, err := s.Append(t.Context(), m)
	if err != nil || !inserted {
		t.Fatalf("append: inserted=%v err=%v", inserted, err)
	}
	return entry
}

func TestHistoryRoundTripAndIdempotency(t *testing.T) {
	dir := privateTestDir(t)
	s := openTestStore(t, dir)
	m := testMessage(1)
	want := appendTest(t, s, m)
	if want.Sequence <= 0 || want.RecordedAt.Location() != time.UTC || !want.RecordedAt.Equal(m.RecordedAt.Truncate(time.Millisecond)) {
		t.Fatal("record has invalid sequence or time")
	}
	m.RecordedAt = m.RecordedAt.Add(time.Hour)
	got, inserted, err := s.Append(t.Context(), m)
	if err != nil || inserted || got != want {
		t.Fatalf("retry changed original: inserted=%v err=%v", inserted, err)
	}
	m.Text = "different content"
	if got, inserted, err := s.Append(t.Context(), m); got != (history.Entry{}) || inserted || !errors.Is(err, history.ErrConflict) {
		t.Fatal("conflicting ID was accepted", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, dir)
	got, err = s.Get(t.Context(), want.Peer, want.Direction, want.ID)
	if err != nil || got != want {
		t.Fatal("reopened record changed", err)
	}
	m = testMessage(1)
	m.Direction = history.Outgoing
	appendTest(t, s, m)
	m.Peer = [32]byte{3}
	appendTest(t, s, m)
	page, err := s.List(t.Context(), testPeer, history.Query{})
	if err != nil || len(page.Entries) != 2 || page.NextBefore != 0 || page.Entries[1] != want {
		t.Fatal("ID scope or listing is wrong", err)
	}
	if _, err := s.Get(t.Context(), testPeer, history.Incoming, protocol.MessageID{99}); !errors.Is(err, history.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestHistoryPaginationSurvivesNewRowsAndClockChanges(t *testing.T) {
	s := openTestStore(t, privateTestDir(t))
	for i := byte(1); i <= 7; i++ {
		m := testMessage(i)
		m.RecordedAt = m.RecordedAt.Add(-time.Duration(i) * time.Hour)
		appendTest(t, s, m)
	}
	first, err := s.List(t.Context(), testPeer, history.Query{Limit: 3})
	if err != nil || len(first.Entries) != 3 || first.Entries[0].ID[0] != 7 || first.NextBefore != 5 {
		t.Fatal("first page", err)
	}
	appendTest(t, s, testMessage(8))
	second, err := s.List(t.Context(), testPeer, history.Query{Limit: 3, Before: first.NextBefore})
	if err != nil || len(second.Entries) != 3 || second.Entries[0].ID[0] != 4 || second.NextBefore != 2 {
		t.Fatal("second page shifted", err)
	}
	last, err := s.List(t.Context(), testPeer, history.Query{Limit: 3, Before: second.NextBefore})
	if err != nil || len(last.Entries) != 1 || last.Entries[0].ID[0] != 1 || last.NextBefore != 0 {
		t.Fatal("last page", err)
	}
	empty, err := s.List(t.Context(), [32]byte{9}, history.Query{})
	if err != nil || len(empty.Entries) != 0 || empty.NextBefore != 0 {
		t.Fatal("empty peer", err)
	}
}

func TestHistoryValidatesBeforeWriting(t *testing.T) {
	s := openTestStore(t, privateTestDir(t))
	for _, change := range []func(*history.Message){
		func(m *history.Message) { m.Peer = [32]byte{} },
		func(m *history.Message) { m.Peer = testOwner },
		func(m *history.Message) { m.Direction = 0 },
		func(m *history.Message) { m.ID = protocol.MessageID{} },
		func(m *history.Message) { m.Text = "" },
		func(m *history.Message) { m.Text = string([]byte{0xff}) },
		func(m *history.Message) { m.Text = strings.Repeat("x", protocol.MaxTextBytes+1) },
		func(m *history.Message) { m.RecordedAt = time.Time{} },
		func(m *history.Message) { m.RecordedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
	} {
		m := testMessage(1)
		change(&m)
		if _, inserted, err := s.Append(t.Context(), m); inserted || !errors.Is(err, history.ErrInvalidMessage) {
			t.Fatal("invalid message accepted", err)
		}
	}
	for _, query := range []history.Query{{Limit: -1}, {Limit: history.MaxPageSize + 1}, {Before: -1}} {
		if _, err := s.List(t.Context(), testPeer, query); !errors.Is(err, history.ErrInvalidQuery) {
			t.Fatal("invalid query accepted", err)
		}
	}
	if _, err := s.Get(t.Context(), testOwner, history.Incoming, protocol.MessageID{1}); !errors.Is(err, history.ErrInvalidQuery) {
		t.Fatal(err)
	}
	page, err := s.List(t.Context(), testPeer, history.Query{})
	if err != nil || len(page.Entries) != 0 {
		t.Fatal("invalid operation wrote a record", err)
	}
	m := testMessage(1)
	m.Text = strings.Repeat("é", protocol.MaxTextBytes/2)
	want := appendTest(t, s, m)
	got, err := s.Get(t.Context(), m.Peer, m.Direction, m.ID)
	if err != nil || got != want {
		t.Fatal("maximum UTF-8 message", err)
	}
}

func TestHistoryConcurrentDuplicatesAndClose(t *testing.T) {
	s := openTestStore(t, privateTestDir(t))
	var added atomic.Int32
	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			_, inserted, err := s.Append(t.Context(), testMessage(1))
			if err != nil {
				t.Error(err)
			}
			if inserted {
				added.Add(1)
			}
		})
	}
	wg.Wait()
	if added.Load() != 1 {
		t.Fatal("concurrent duplicates committed", added.Load())
	}
	for range 12 {
		wg.Go(func() {
			_, err := s.List(t.Context(), testPeer, history.Query{})
			if err != nil && !errors.Is(err, history.ErrClosed) {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if _, _, err := s.Append(t.Context(), testMessage(2)); !errors.Is(err, history.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := s.Get(t.Context(), testPeer, history.Incoming, protocol.MessageID{1}); !errors.Is(err, history.ErrClosed) {
		t.Fatal(err)
	}
}

func TestHistoryCancellationAndLifetimeLock(t *testing.T) {
	dir := privateTestDir(t)
	s := openTestStore(t, dir)
	if other, err := Open(t.Context(), dir, testOwner, testRoot()); other != nil || !errors.Is(err, history.ErrBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatal("second store acquired lock", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := s.Append(ctx, testMessage(1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.List(ctx, testPeer, history.Query{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, testPeer, history.Incoming, protocol.MessageID{1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := Open(ctx, filepath.Join(dir, "canceled"), testOwner, testRoot()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "canceled")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled open created files")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	s, err := Open(ctx, dir, testOwner, testRoot())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	cancel()
	appendTest(t, s, testMessage(1)) // Startup context must not own the store.
}

func TestHistoryFailedWriteRollsBackAllocatedRecord(t *testing.T) {
	s := openTestStore(t, privateTestDir(t))
	want := appendTest(t, s, testMessage(1))
	// Keep the current pages but prevent allocation of the overflow pages needed
	// for a maximum-size ciphertext. This fails after the placeholder INSERT.
	var pages int
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages)); err != nil {
		t.Fatal(err)
	}
	m := testMessage(2)
	m.Text = strings.Repeat("x", protocol.MaxTextBytes)
	if entry, added, err := s.Append(t.Context(), m); entry != (history.Entry{}) || added || !errors.Is(err, history.ErrStorage) {
		t.Fatal("full database accepted append", err)
	}
	if _, err := s.Get(t.Context(), m.Peer, m.Direction, m.ID); !errors.Is(err, history.ErrNotFound) {
		t.Fatal("failed write left a placeholder", err)
	}
	page, err := s.List(t.Context(), m.Peer, history.Query{})
	if err != nil || len(page.Entries) != 1 || page.Entries[0] != want {
		t.Fatal("failed write damaged earlier history", err)
	}
	if _, err := s.db.Exec("PRAGMA max_page_count=1000"); err != nil {
		t.Fatal(err)
	}
	appendTest(t, s, m)
}

func TestHistoryNeverWritesPlaintextToDatabaseOrSidecars(t *testing.T) {
	dir := privateTestDir(t)
	s := openTestStore(t, dir)
	m := testMessage(1)
	m.Text = "unique plaintext marker 68409d7e Ω сообщения"
	appendTest(t, s, m)
	m.ID[0] = 2
	appendTest(t, s, m)
	var first, second []byte
	if err := s.db.QueryRow("SELECT ciphertext FROM messages WHERE sequence=1").Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT ciphertext FROM messages WHERE sequence=2").Scan(&second); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) || len(first) != len(m.Text)+40 {
		t.Fatal("encryption nonce reused or ciphertext length is wrong")
	}
	checkFiles := func() {
		t.Helper()
		for _, name := range []string{fileName, fileName + "-wal", fileName + "-shm", fileName + "-journal"} {
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte(m.Text)) || bytes.Contains(data, testRoot()) {
				t.Fatal("plaintext or root key leaked to", name)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
				t.Fatal("nonprivate SQLite file:", name, info.Mode())
			}
		}
	}
	checkFiles()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	checkFiles()
}
