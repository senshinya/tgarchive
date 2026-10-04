package store

import (
	"database/sql"
	"errors"
)

var ErrNotFound = errors.New("not found")

const (
	StatusRunning = "running"
	StatusError   = "error"
	StatusStopped = "stopped"
	StatusRemoved = "removed"

	StatePending  = "pending"
	StateDone     = "done"
	StateFailed   = "failed"
	StateTooLarge = "too_large"

	ReceiptNone   = "none"
	ReceiptSeen   = "seen"
	ReceiptDone   = "done"
	ReceiptFailed = "failed"
)

type scanner interface{ Scan(dest ...any) error }

// affected turns "0 rows affected" into ErrNotFound.
func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
