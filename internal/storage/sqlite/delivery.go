package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/bacrehclb/ShadowTalk/internal/delivery"
	"github.com/bacrehclb/ShadowTalk/internal/history"
	"github.com/bacrehclb/ShadowTalk/internal/protocol"
)

var _ delivery.Store = (*Store)(nil)

const itemColumns = "m.sequence, substr(m.peer,1,33), m.direction, substr(m.message_id,1,17), m.recorded_at, substr(m.ciphertext,1,16425), o.attempts, o.next_attempt, o.delivered_at"
const selectItem = "SELECT " + itemColumns + " FROM messages m JOIN outbox o ON o.sequence=m.sequence"

func validDeliveryTime(t time.Time) bool { return t.UTC().Year() >= 1970 && t.UTC().Year() <= 9999 }

func (s *Store) Enqueue(ctx context.Context, m history.Message) (delivery.Item, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.ready(ctx); err != nil {
		return delivery.Item{}, false, err
	}
	if m.Validate() != nil || m.Direction != history.Outgoing || !validPeer(m.Peer, s.owner) {
		return delivery.Item{}, false, history.ErrInvalidMessage
	}
	m.RecordedAt = time.UnixMilli(m.RecordedAt.UnixMilli()).UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return delivery.Item{}, false, storageError(err)
	}
	defer tx.Rollback()
	entry, _, err := s.appendRecord(ctx, tx, m)
	if err != nil {
		return delivery.Item{}, false, err
	}
	item, err := s.readItem(tx.QueryRowContext(ctx, selectItem+" WHERE m.sequence=?", entry.Sequence))
	queued := false
	if errors.Is(err, history.ErrNotFound) {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM outbox WHERE delivered_at IS NULL").Scan(&count); err != nil {
			return delivery.Item{}, false, storageError(err)
		}
		if count >= delivery.MaxPendingMessages {
			return delivery.Item{}, false, delivery.ErrQueueFull
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO outbox(sequence,next_attempt) VALUES(?,?)", entry.Sequence, m.RecordedAt.UnixMilli()); err != nil {
			return delivery.Item{}, false, storageError(err)
		}
		item = delivery.Item{Entry: entry, State: delivery.Pending, NextAttempt: m.RecordedAt}
		queued = true
	} else if err != nil {
		return delivery.Item{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return delivery.Item{}, false, storageError(err)
	}
	return item, queued, nil
}

func (s *Store) Status(ctx context.Context, peer [32]byte, id protocol.MessageID) (delivery.Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.ready(ctx); err != nil {
		return delivery.Item{}, err
	}
	if !validPeer(peer, s.owner) || id == (protocol.MessageID{}) {
		return delivery.Item{}, history.ErrInvalidQuery
	}
	return s.readItem(s.db.QueryRowContext(ctx, selectItem+" WHERE m.peer=? AND m.direction=2 AND m.message_id=?", peer[:], id[:]))
}

func (s *Store) Due(ctx context.Context, now time.Time, limit int) ([]delivery.Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	if !validDeliveryTime(now) || limit < 1 || limit > delivery.MaxBatchSize {
		return nil, history.ErrInvalidQuery
	}
	rows, err := s.db.QueryContext(ctx, selectItem+` WHERE o.delivered_at IS NULL AND o.next_attempt<=? AND m.direction=2
AND NOT EXISTS (SELECT 1 FROM messages previous JOIN outbox pending ON pending.sequence=previous.sequence
 WHERE previous.peer=m.peer AND previous.sequence<m.sequence AND pending.delivered_at IS NULL)
ORDER BY o.next_attempt, m.sequence LIMIT ?`, now.UnixMilli(), limit)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	items := make([]delivery.Item, 0, limit)
	for rows.Next() {
		item, err := s.readItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, storageError(err)
	}
	return items, nil
}

func (s *Store) BeginAttempt(ctx context.Context, sequence int64, now, next time.Time) (delivery.Item, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.ready(ctx); err != nil {
		return delivery.Item{}, false, err
	}
	if sequence <= 0 || !validDeliveryTime(now) || !validDeliveryTime(next) || next.UnixMilli() <= now.UnixMilli() {
		return delivery.Item{}, false, history.ErrInvalidQuery
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return delivery.Item{}, false, storageError(err)
	}
	defer tx.Rollback()
	item, err := s.readItem(tx.QueryRowContext(ctx, selectItem+" WHERE m.sequence=?", sequence))
	if err != nil {
		return delivery.Item{}, false, err
	}
	if item.State != delivery.Pending || item.NextAttempt.After(now) {
		return delivery.Item{}, false, nil
	}
	// Saturation keeps long-lived undeliverable records pending without overflow.
	item.Attempts = min(item.Attempts+1, 1000000000)
	item.NextAttempt = time.UnixMilli(next.UnixMilli()).UTC()
	if _, err := tx.ExecContext(ctx, "UPDATE outbox SET attempts=?,next_attempt=? WHERE sequence=?", item.Attempts, next.UnixMilli(), sequence); err != nil {
		return delivery.Item{}, false, storageError(err)
	}
	if err := tx.Commit(); err != nil {
		return delivery.Item{}, false, storageError(err)
	}
	return item, true, nil
}

func (s *Store) Retry(ctx context.Context, sequence, attempts int64, next time.Time) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.ready(ctx); err != nil {
		return err
	}
	if sequence <= 0 || attempts <= 0 || attempts > 1000000000 || !validDeliveryTime(next) {
		return history.ErrInvalidQuery
	}
	_, err := s.db.ExecContext(ctx, "UPDATE outbox SET next_attempt=? WHERE sequence=? AND attempts=? AND delivered_at IS NULL", next.UnixMilli(), sequence, attempts)
	return storageError(err)
}

func (s *Store) Acknowledge(ctx context.Context, peer [32]byte, id protocol.MessageID, now time.Time) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.ready(ctx); err != nil {
		return false, err
	}
	if !validPeer(peer, s.owner) || id == (protocol.MessageID{}) || !validDeliveryTime(now) {
		return false, history.ErrInvalidQuery
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, storageError(err)
	}
	defer tx.Rollback()
	item, err := s.readItem(tx.QueryRowContext(ctx, selectItem+" WHERE m.peer=? AND m.direction=2 AND m.message_id=?", peer[:], id[:]))
	if errors.Is(err, history.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if item.State == delivery.Delivered || item.Attempts == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE outbox SET delivered_at=? WHERE sequence=?", now.UnixMilli(), item.Sequence); err != nil {
		return false, storageError(err)
	}
	if err := tx.Commit(); err != nil {
		return false, storageError(err)
	}
	return true, nil
}

func (s *Store) readItem(row scanner) (delivery.Item, error) {
	var item delivery.Item
	var next int64
	var delivered sql.NullInt64
	entry, err := s.readRecord(row, &item.Attempts, &next, &delivered)
	if err != nil {
		return delivery.Item{}, err
	}
	if entry.Direction != history.Outgoing || item.Attempts < 0 || item.Attempts > 1000000000 || next < 0 || next > 253402300799999 || delivered.Valid && (item.Attempts == 0 || delivered.Int64 < 0 || delivered.Int64 > 253402300799999) {
		return delivery.Item{}, history.ErrCorrupt
	}
	item.Entry = entry
	item.State = delivery.Pending
	item.NextAttempt = time.UnixMilli(next).UTC()
	if delivered.Valid {
		item.State = delivery.Delivered
		item.DeliveredAt = time.UnixMilli(delivered.Int64).UTC()
	}
	return item, nil
}
