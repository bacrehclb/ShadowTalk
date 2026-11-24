// Package sqlite stores profile-owned message history. Only authenticated
// ciphertext is passed to SQLite; public message metadata remains visible.
package sqlite

import (
	"bytes"
	"context"
	"crypto/cipher"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/gofrs/flock"
	_ "modernc.org/sqlite"
)

const fileName = "messages.db"

type Store struct {
	mu       sync.RWMutex
	db       *sql.DB
	lock     *flock.Flock
	owner    [32]byte
	aead     cipher.AEAD
	closed   bool
	closeErr error
}

var _ history.Store = (*Store)(nil)

// Open copies/derives from rootKey and never owns the caller's key bytes.
// ctx covers startup only. Close the store explicitly; each operation has its
// own context. One store owns the directory's history lock for its lifetime.
func Open(ctx context.Context, directory string, owner [32]byte, rootKey []byte) (_ *Store, err error) {
	if ctx == nil || directory == "" || owner == ([32]byte{}) || len(rootKey) != 32 || bytes.Equal(rootKey, make([]byte, 32)) {
		return nil, history.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	lock, err := prepare(directory)
	if err != nil {
		return nil, err
	}
	s := &Store{lock: lock, owner: owner}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.Close())
		}
	}()
	s.db, err = sql.Open("sqlite", databaseURL(filepath.Join(directory, fileName)))
	if err != nil {
		return nil, storageError(err)
	}
	s.db.SetMaxOpenConns(1)
	s.db.SetMaxIdleConns(1)
	if err := s.initialize(ctx, rootKey); err != nil {
		return nil, err
	}
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return nil, storageError(err)
	}
	if mode != "wal" {
		return nil, history.ErrStorage
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s, nil
}

func databaseURL(path string) string {
	path = filepath.ToSlash(path)
	// SQLite accepts /C:/... for a Windows drive; URL escaping keeps filename
	// punctuation from becoming driver options or SQL pragmas.
	if len(path) >= 2 && path[1] == ':' {
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{"mode": {"rw"}, "_txlock": {"immediate"}, "_defensive": {"true"}, "_dqs": {"false"}}
	for _, pragma := range []string{"busy_timeout(5000)", "synchronous(FULL)", "foreign_keys(ON)", "trusted_schema(OFF)", "temp_store(MEMORY)"} {
		q.Add("_pragma", pragma)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Close waits for active operations, closes SQLite before releasing the file
// lock, and releases the cipher reference. It is repeatable and concurrent-safe.
// Go does not guarantee erasure of every in-memory key/plaintext copy.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.closeErr
	}
	s.closed = true
	if s.db != nil {
		s.closeErr = storageError(s.db.Close())
	}
	if s.lock != nil {
		s.closeErr = errors.Join(s.closeErr, s.lock.Close())
	}
	s.aead = nil
	return s.closeErr
}

// Called with mu held. A store's context is intentionally not the startup ctx.
func (s *Store) ready(ctx context.Context) error {
	if ctx == nil {
		return history.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed {
		return history.ErrClosed
	}
	return nil
}

func validPeer(peer, owner [32]byte) bool { return peer != ([32]byte{}) && peer != owner }

// Keep URI support to local files: shared/network filesystems are not a
// supported SQLite WAL deployment for this profile-local store.
func localDirectory(path string) bool { return !strings.HasPrefix(filepath.ToSlash(path), "//") }
