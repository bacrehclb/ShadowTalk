package sqlite

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"errors"

	"github.com/bacrehclb/ShadowTalk/internal/history"
)

const schemaVersion = 1
const applicationID = 0x53544831 // STH1

//go:embed migrations/0001_messages.sql
var initialSchema string

func (s *Store) initialize(ctx context.Context, root []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(err)
	}
	defer tx.Rollback()
	var appID, version, objects int
	if err := tx.QueryRowContext(ctx, "PRAGMA application_id").Scan(&appID); err != nil {
		return storageError(err)
	}
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return storageError(err)
	}
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*'").Scan(&objects); err != nil {
		return storageError(err)
	}
	if appID == 0 && version == 0 && objects == 0 {
		if err := s.create(ctx, tx, root); err != nil {
			return err
		}
	} else {
		if appID != applicationID {
			return history.ErrForeignDatabase
		}
		if version > schemaVersion {
			return history.ErrUnsupportedVersion
		}
		if version != schemaVersion {
			return history.ErrCorrupt
		}
		if err := s.loadKey(ctx, tx, root); err != nil {
			return err
		}
	}
	if err := validateSchema(ctx, tx); err != nil {
		return err
	}
	return storageError(tx.Commit())
}

func (s *Store) create(ctx context.Context, tx *sql.Tx, root []byte) error {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	aead, err := databaseCipher(root, salt)
	if err != nil {
		return err
	}
	check, err := seal(aead, nil, keyCheckAAD(s.owner, salt))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, initialSchema); err != nil {
		return storageError(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO history_meta(singleton, owner, salt, key_check) VALUES(1, ?, ?, ?)", s.owner[:], salt, check); err != nil {
		return storageError(err)
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA application_id=1398032433; PRAGMA user_version=1"); err != nil {
		return storageError(err)
	}
	s.aead = aead
	return nil
}

func (s *Store) loadKey(ctx context.Context, tx *sql.Tx, root []byte) error {
	var owner, salt, check []byte
	// Bound returned blobs even if a damaged schema no longer enforces lengths.
	err := tx.QueryRowContext(ctx, "SELECT substr(owner,1,33), substr(salt,1,33), substr(key_check,1,41) FROM history_meta WHERE singleton=1").Scan(&owner, &salt, &check)
	if errors.Is(err, sql.ErrNoRows) {
		return history.ErrCorrupt
	}
	if err != nil {
		return storageError(err)
	}
	if len(owner) != 32 || len(salt) != 32 || len(check) != 40 {
		return history.ErrCorrupt
	}
	if !bytes.Equal(owner, s.owner[:]) {
		return history.ErrOwnerMismatch
	}
	aead, err := databaseCipher(root, salt)
	if err != nil {
		return err
	}
	plain, err := unseal(aead, check, keyCheckAAD(s.owner, salt))
	defer clear(plain)
	if err != nil {
		return err
	}
	if len(plain) != 0 {
		return history.ErrCorrupt
	}
	s.aead = aead
	return nil
}

func validateSchema(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT type, name FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*'")
	if err != nil {
		return storageError(err)
	}
	expected := map[string]string{"history_meta": "table", "messages": "table", "messages_peer_sequence": "index"}
	for rows.Next() {
		var kind, name string
		if err := rows.Scan(&kind, &name); err != nil {
			_ = rows.Close()
			return storageError(err)
		}
		if expected[name] != kind {
			_ = rows.Close()
			return history.ErrCorrupt
		}
		delete(expected, name)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return storageError(err)
	}
	if len(expected) != 0 {
		return history.ErrCorrupt
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM history_meta").Scan(&count); err != nil {
		return storageError(err)
	}
	if count != 1 {
		return history.ErrCorrupt
	}
	rows, err = tx.QueryContext(ctx, "SELECT sequence, peer, direction, message_id, recorded_at, ciphertext FROM messages LIMIT 0")
	if err != nil {
		return history.ErrCorrupt
	}
	return storageError(rows.Close())
}
