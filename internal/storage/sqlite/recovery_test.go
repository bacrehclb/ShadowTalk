package sqlite

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/history"
)

// The child exits without Store.Close, bypassing Go defers. This exercises WAL
// recovery and OS lock release after a process exit, not hardware power loss.
func TestHistoryProcessExitRecovery(t *testing.T) {
	if directory := os.Getenv("SHADOWTALK_HISTORY_TEST_DIRECTORY"); directory != "" {
		s, err := Open(context.Background(), directory, testOwner, testRoot())
		if os.Getenv("SHADOWTALK_HISTORY_TEST_LOCKED") == "1" {
			if s != nil || !errors.Is(err, history.ErrBusy) {
				t.Fatal("child bypassed lifetime lock", err)
			}
			os.Exit(0)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, added, err := s.Append(context.Background(), testMessage(1)); err != nil || !added {
			t.Fatal("child commit", err)
		}
		queued, _, err := s.Enqueue(context.Background(), outgoing(3))
		if err != nil {
			t.Fatal(err)
		}
		now := queued.RecordedAt.Add(time.Minute)
		if _, _, err := s.BeginAttempt(context.Background(), queued.Sequence, now, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		tx, err := s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		// An allocated but unfinished record must not survive the process.
		m := testMessage(2)
		if _, err := tx.Exec("INSERT INTO messages(peer,direction,message_id,recorded_at,ciphertext) VALUES(?,?,?,?,zeroblob(41))", m.Peer[:], m.Direction, m.ID[:], m.RecordedAt.UnixMilli()); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	dir := privateTestDir(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestHistoryProcessExitRecovery$")
	child.Env = append(os.Environ(), "SHADOWTALK_HISTORY_TEST_DIRECTORY="+dir)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
	if info, err := os.Stat(filepath.Join(dir, fileName+"-wal")); err != nil || info.Size() == 0 {
		t.Fatal("child did not leave a WAL to recover", err)
	}
	s := openTestStore(t, dir)
	m := testMessage(1)
	got, err := s.Get(t.Context(), m.Peer, m.Direction, m.ID)
	if err != nil || got.Text != m.Text {
		t.Fatal("committed message lost", err)
	}
	m = testMessage(2)
	if _, err := s.Get(t.Context(), m.Peer, m.Direction, m.ID); !errors.Is(err, history.ErrNotFound) {
		t.Fatal("uncommitted placeholder survived", err)
	}
	appendTest(t, s, m)
	// A crash after recording an attempt cannot leave an eternal in-flight row.
	now := outgoing(3).RecordedAt.Add(time.Minute)
	if items, err := s.Due(t.Context(), now, 10); err != nil || len(items) != 0 {
		t.Fatal("crash lost recovery deadline", err)
	}
	items, err := s.Due(t.Context(), now.Add(2*time.Minute), 10)
	if err != nil || len(items) != 1 || items[0].ID != outgoing(3).ID || items[0].Attempts != 1 {
		t.Fatal("crashed attempt did not become retryable", err)
	}
}

func TestHistoryLifetimeLockAcrossProcesses(t *testing.T) {
	dir := privateTestDir(t)
	s := openTestStore(t, dir)
	appendTest(t, s, testMessage(1))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestHistoryProcessExitRecovery$")
	child.Env = append(os.Environ(), "SHADOWTALK_HISTORY_TEST_DIRECTORY="+dir, "SHADOWTALK_HISTORY_TEST_LOCKED=1")
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
}
