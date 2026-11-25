package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
)

// Bound blobs even when the database's constraints have been damaged.
const recordColumns = "sequence, substr(peer,1,33), direction, substr(message_id,1,17), recorded_at, substr(ciphertext,1,16425)"
const findRecord = "SELECT " + recordColumns + " FROM messages WHERE peer=? AND direction=? AND message_id=?"

func (s *Store) Append(ctx context.Context, message history.Message) (history.Entry, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.ready(ctx); err != nil {
		return history.Entry{}, false, err
	}
	if message.Validate() != nil || !validPeer(message.Peer, s.owner) {
		return history.Entry{}, false, history.ErrInvalidMessage
	}
	message.RecordedAt = time.UnixMilli(message.RecordedAt.UnixMilli()).UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return history.Entry{}, false, storageError(err)
	}
	defer tx.Rollback()
	entry, inserted, err := s.appendRecord(ctx, tx, message)
	if err != nil {
		return history.Entry{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return history.Entry{}, false, storageError(err)
	}
	return entry, inserted, nil
}

// Caller validates the message and owns the transaction.
func (s *Store) appendRecord(ctx context.Context, tx *sql.Tx, message history.Message) (history.Entry, bool, error) {
	existing, err := s.readRecord(tx.QueryRowContext(ctx, findRecord, message.Peer[:], message.Direction, message.ID[:]))
	if err == nil {
		if existing.Text != message.Text {
			return history.Entry{}, false, history.ErrConflict
		}
		return existing, false, nil
	}
	if !errors.Is(err, history.ErrNotFound) {
		return history.Entry{}, false, err
	}
	// Allocate the insertion sequence inside the transaction so it can be
	// authenticated with the message. The placeholder never commits: encryption
	// and replacement must both succeed first. No plaintext enters SQL.
	result, err := tx.ExecContext(ctx, "INSERT INTO messages(peer,direction,message_id,recorded_at,ciphertext) VALUES(?,?,?,?,zeroblob(41))", message.Peer[:], message.Direction, message.ID[:], message.RecordedAt.UnixMilli())
	if err != nil {
		return history.Entry{}, false, storageError(err)
	}
	sequence, err := result.LastInsertId()
	if err != nil || sequence <= 0 {
		return history.Entry{}, false, history.ErrStorage
	}
	entry := history.Entry{Sequence: sequence, Message: message}
	plain := []byte(message.Text)
	ciphertext, err := seal(s.aead, plain, s.messageAAD(entry))
	clear(plain)
	if err != nil {
		return history.Entry{}, false, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE messages SET ciphertext=? WHERE sequence=?", ciphertext, sequence); err != nil {
		return history.Entry{}, false, storageError(err)
	}
	return entry, true, nil
}

func (s *Store) Get(ctx context.Context, peer [32]byte, direction history.Direction, id protocol.MessageID) (history.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.ready(ctx); err != nil {
		return history.Entry{}, err
	}
	if !validPeer(peer, s.owner) || (direction != history.Incoming && direction != history.Outgoing) || id == (protocol.MessageID{}) {
		return history.Entry{}, history.ErrInvalidQuery
	}
	return s.readRecord(s.db.QueryRowContext(ctx, findRecord, peer[:], direction, id[:]))
}

func (s *Store) List(ctx context.Context, peer [32]byte, query history.Query) (history.Page, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.ready(ctx); err != nil {
		return history.Page{}, err
	}
	if !validPeer(peer, s.owner) || query.Validate() != nil {
		return history.Page{}, history.ErrInvalidQuery
	}
	limit := query.Limit
	if limit == 0 {
		limit = history.DefaultPageSize
	}
	statement := "SELECT " + recordColumns + " FROM messages WHERE peer=?"
	args := []any{peer[:]}
	if query.Before != 0 {
		statement += " AND sequence<?"
		args = append(args, query.Before)
	}
	statement += " ORDER BY sequence DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return history.Page{}, storageError(err)
	}
	defer rows.Close()
	page := history.Page{Entries: make([]history.Entry, 0, limit)}
	for rows.Next() {
		entry, err := s.readRecord(rows)
		if err != nil {
			return history.Page{}, err
		}
		if len(page.Entries) == limit {
			page.NextBefore = page.Entries[limit-1].Sequence
		} else {
			page.Entries = append(page.Entries, entry)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return history.Page{}, storageError(err)
	}
	return page, nil
}

type scanner interface{ Scan(...any) error }

func (s *Store) readRecord(row scanner, extra ...any) (history.Entry, error) {
	var entry history.Entry
	var peer, id, ciphertext []byte
	var direction, millis int64
	fields := []any{&entry.Sequence, &peer, &direction, &id, &millis, &ciphertext}
	err := row.Scan(append(fields, extra...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return history.Entry{}, history.ErrNotFound
	}
	if err != nil {
		return history.Entry{}, storageError(err)
	}
	if len(peer) != 32 || len(id) != 16 || (direction != int64(history.Incoming) && direction != int64(history.Outgoing)) || millis < 0 || millis > 253402300799999 {
		return history.Entry{}, history.ErrCorrupt
	}
	copy(entry.Peer[:], peer)
	copy(entry.ID[:], id)
	entry.Direction = history.Direction(direction)
	entry.RecordedAt = time.UnixMilli(millis).UTC()
	return s.decrypt(entry, ciphertext)
}
