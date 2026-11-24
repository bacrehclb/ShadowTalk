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
