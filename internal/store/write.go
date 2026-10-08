package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"time"

	"modernc.org/sqlite"
)

var ErrWriteBusy = errors.New("数据库写入繁忙，请稍后重试")

// WriteTx waits for BEGIN IMMEDIATE in Go, rather than SQLite's native busy
// handler, so cancellation can stop a waiting request. fn must only use tx and
// have no external side effects. Total write budget is 10s; BUSY from an acquired
// transaction is rolled back and retried at most three times.
func WriteTx(parent context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return writeError(parent, ctx, err)
	}
	defer conn.Close()
	// This connection is exclusively held until its normal timeout is restored.
	defer func() {
		reset, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if _, err := conn.ExecContext(reset, `PRAGMA busy_timeout(10000)`); err != nil {
			// A cancelled transaction may have already discarded the connection.
			// Never return an incorrectly configured/live transaction to the pool.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	if _, err = conn.ExecContext(ctx, `PRAGMA busy_timeout(0)`); err != nil {
		return writeError(parent, ctx, err)
	}
	for retries := 0; ; {
		if err = ctx.Err(); err != nil {
			return writeError(parent, ctx, err)
		}
		tx, beginErr := conn.BeginTx(ctx, nil)
		if beginErr == nil {
			err = applyWrite(ctx, tx, fn)
			if err == nil {
				return nil
			}
			if !sqliteBusy(err) || retries == 3 {
				return writeError(parent, ctx, err)
			}
			retries++
		} else if !sqliteBusy(beginErr) {
			return writeError(parent, ctx, beginErr)
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return writeError(parent, ctx, ctx.Err())
		case <-timer.C:
		}
	}
}

func applyWrite(ctx context.Context, tx *sql.Tx, fn func(*sql.Tx) error) error {
	defer tx.Rollback()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// modernc v1.60 cleans up its native transaction after a failed COMMIT.
	// database/sql has already marked Tx done, so do not issue another ROLLBACK:
	// that would discard a clean connection and break the next retry.
	return tx.Commit()
}

func sqliteBusy(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&255 == 5
}

func writeError(parent, ctx context.Context, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if ctx.Err() != nil || sqliteBusy(err) {
		return ErrWriteBusy
	}
	return err
}
