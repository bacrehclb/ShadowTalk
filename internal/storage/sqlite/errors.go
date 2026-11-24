package sqlite

import (
	"context"
	"errors"
	"fmt"

	"github.com/bacrehclb/ShadowTalk/internal/history"
	driver "modernc.org/sqlite"
)

// Never expose driver messages: a modified schema can put arbitrary text in
// them. Keep only the numeric SQLite result code and useful sentinel errors.
func storageError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var sqliteErr *driver.Error
	if errors.As(err, &sqliteErr) {
		cause := history.ErrStorage
		switch sqliteErr.Code() & 0xff {
		case 5, 6:
			cause = history.ErrBusy // SQLITE_BUSY, SQLITE_LOCKED
		case 11, 19, 26:
			cause = history.ErrCorrupt // CORRUPT, CONSTRAINT, NOTADB
		}
		return fmt.Errorf("%w (SQLite %d)", cause, sqliteErr.Code())
	}
	return history.ErrStorage
}
