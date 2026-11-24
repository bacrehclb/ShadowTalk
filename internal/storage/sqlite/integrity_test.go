package sqlite

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bacrehclb/ShadowTalk/internal/history"
)

func TestHistoryRejectsWrongOwnerAndKeyWithoutChangingData(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
			dir := privateTestDir(t)
			s := openTestStore(t, dir)
			if populated {
				appendTest(t, s, testMessage(1))
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			for _, test := range []struct {
				owner [32]byte
				key   []byte
				want  error
			}{
				{testOwner, bytes.Repeat([]byte{0x33}, 32), history.ErrDecryption},
				{[32]byte{3}, testRoot(), history.ErrOwnerMismatch},
			} {
				other, err := Open(t.Context(), dir, test.owner, test.key)
				if other != nil || !errors.Is(err, test.want) {
					if other != nil {
						other.Close()
					}
					t.Fatal("wrong credentials accepted", err)
				}
			}
			s = openTestStore(t, dir) // Failed opens must release the lifetime lock.
			page, err := s.List(t.Context(), testPeer, history.Query{})
			want := 0
			if populated {
				want = 1
			}
			if err != nil || len(page.Entries) != want {
				t.Fatal("failed open changed the records", err)
			}
		})
	}
}

func TestHistoryAuthenticatesEveryRecordField(t *testing.T) {
	for _, field := range []string{"ciphertext", "nonce", "sequence", "peer", "direction", "message_id", "recorded_at", "swap-ciphertext"} {
		t.Run(field, func(t *testing.T) {
			s := openTestStore(t, privateTestDir(t))
			entry := appendTest(t, s, testMessage(1))
			statement := ""
			var value any
			switch field {
			case "ciphertext", "nonce":
				var encrypted []byte
				if err := s.db.QueryRow("SELECT ciphertext FROM messages WHERE sequence=1").Scan(&encrypted); err != nil {
					t.Fatal(err)
				}
				at := len(encrypted) - 1
				if field == "nonce" {
					at = 0
				}
				encrypted[at] ^= 1
				statement, value = "UPDATE messages SET ciphertext=? WHERE sequence=1", encrypted
			case "sequence":
				statement, value = "UPDATE messages SET sequence=? WHERE sequence=1", 17
			case "peer":
				entry.Peer = [32]byte{3}
				statement, value = "UPDATE messages SET peer=? WHERE sequence=1", entry.Peer[:]
			case "direction":
				entry.Direction = history.Outgoing
				statement, value = "UPDATE messages SET direction=? WHERE sequence=1", entry.Direction
			case "message_id":
				entry.ID[0] = 9
				statement, value = "UPDATE messages SET message_id=? WHERE sequence=1", entry.ID[:]
			case "recorded_at":
				statement, value = "UPDATE messages SET recorded_at=? WHERE sequence=1", entry.RecordedAt.UnixMilli()+1
			case "swap-ciphertext":
				appendTest(t, s, testMessage(2))
				statement, value = "UPDATE messages SET ciphertext=(SELECT ciphertext FROM messages WHERE sequence=2) WHERE sequence=?", 1
			}
			if _, err := s.db.Exec(statement, value); err != nil {
				t.Fatal(err)
			}
			if got, err := s.Get(t.Context(), entry.Peer, entry.Direction, entry.ID); got != (history.Entry{}) || !errors.Is(err, history.ErrDecryption) {
				t.Fatal("tampering accepted by Get", err)
			}
			if page, err := s.List(t.Context(), entry.Peer, history.Query{}); len(page.Entries) != 0 || !errors.Is(err, history.ErrCorrupt) {
				t.Fatal("tampered page returned plaintext", err)
			}
			if _, added, err := s.Append(t.Context(), entry.Message); added || !errors.Is(err, history.ErrCorrupt) {
				t.Fatal("tampered duplicate overwritten", err)
			}
		})
	}
}

func TestHistoryCiphertextCannotMoveBetweenDatabases(t *testing.T) {
	first := openTestStore(t, privateTestDir(t))
	second := openTestStore(t, privateTestDir(t))
	entry := appendTest(t, first, testMessage(1))
	appendTest(t, second, testMessage(1))
	var encrypted []byte
	if err := first.db.QueryRow("SELECT ciphertext FROM messages WHERE sequence=1").Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if _, err := second.db.Exec("UPDATE messages SET ciphertext=? WHERE sequence=1", encrypted); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Get(t.Context(), entry.Peer, entry.Direction, entry.ID); !errors.Is(err, history.ErrDecryption) {
		t.Fatal("ciphertext authenticated in a different database", err)
	}
}

func rawDatabase(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", databaseURL(filepath.Join(dir, fileName)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestHistoryNeverResetsUnsupportedOrDamagedDatabases(t *testing.T) {
	for _, kind := range []string{"future", "foreign", "missing-table", "extra-trigger", "salt", "key-check", "corrupt-file"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateTestDir(t)
			s := openTestStore(t, dir)
			appendTest(t, s, testMessage(1))
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			want := history.ErrCorrupt
			if kind == "corrupt-file" {
				if err := os.WriteFile(filepath.Join(dir, fileName), []byte("not a SQLite database"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				db := rawDatabase(t, dir)
				statement := ""
				switch kind {
				case "future":
					statement = "PRAGMA user_version=2"
					want = history.ErrUnsupportedVersion
				case "foreign":
					statement = "PRAGMA application_id=123"
					want = history.ErrForeignDatabase
				case "missing-table":
					statement = "DROP TABLE messages"
				case "extra-trigger":
					statement = "CREATE TRIGGER sqliteXunexpected AFTER INSERT ON messages BEGIN DELETE FROM messages; END"
				case "salt":
					statement = "UPDATE history_meta SET salt=zeroblob(32)"
					want = history.ErrDecryption
				case "key-check":
					statement = "UPDATE history_meta SET key_check=zeroblob(40)"
					want = history.ErrDecryption
				}
				if _, err := db.Exec(statement); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(filepath.Join(dir, fileName))
			if err != nil {
				t.Fatal(err)
			}
			other, err := Open(t.Context(), dir, testOwner, testRoot())
			if other != nil || !errors.Is(err, want) {
				if other != nil {
					other.Close()
				}
				t.Fatal("bad database accepted", err)
			}
			after, err := os.ReadFile(filepath.Join(dir, fileName))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("failed open rewrote database")
			}
		})
	}
}

func TestHistorySafePathsAndPermissions(t *testing.T) {
	t.Run("escaped filename", func(t *testing.T) {
		name := "история # % & _pragma=oops"
		if runtime.GOOS != "windows" {
			name += "?mode=memory"
		}
		dir := filepath.Join(privateTestDir(t), name)
		s := openTestStore(t, dir)
		want := appendTest(t, s, testMessage(1))
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s = openTestStore(t, dir)
		got, err := s.Get(t.Context(), want.Peer, want.Direction, want.ID)
		if err != nil || got != want {
			t.Fatal("URI punctuation changed the database path", err)
		}
	})
	t.Run("nonregular file", func(t *testing.T) {
		dir := privateTestDir(t)
		if err := os.Mkdir(filepath.Join(dir, fileName), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(t.Context(), dir, testOwner, testRoot()); !errors.Is(err, history.ErrUnsafePath) {
			t.Fatal(err)
		}
	})
	if runtime.GOOS == "windows" {
		return
	} // Unix modes and unprivileged symlinks.
	for _, name := range []string{fileName, fileName + "-wal", ".messages.lock"} {
		t.Run("symlink "+name, func(t *testing.T) {
			dir := privateTestDir(t)
			target := filepath.Join(privateTestDir(t), "target")
			if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(t.Context(), dir, testOwner, testRoot()); !errors.Is(err, history.ErrUnsafePath) {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(target)
			if string(data) != "untouched" {
				t.Fatal("followed symlink")
			}
		})
	}
	t.Run("public directory", func(t *testing.T) {
		dir := privateTestDir(t)
		if err := os.Chmod(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(t.Context(), dir, testOwner, testRoot()); !errors.Is(err, history.ErrUnsafePath) {
			t.Fatal(err)
		}
	})
}
