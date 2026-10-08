package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteTxWaitsForOtherPoolAndHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")
	db, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	second, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, e = db.Exec(`PRAGMA busy_timeout=1;CREATE TABLE retry_probe(id INTEGER PRIMARY KEY)`); e != nil {
		t.Fatal(e)
	}
	lock, e := second.Begin()
	if e != nil {
		t.Fatal(e)
	}
	released := make(chan error, 1)
	go func() { time.Sleep(50 * time.Millisecond); released <- lock.Rollback() }()
	e = WriteTx(context.Background(), db, func(tx *sql.Tx) error { _, e := tx.Exec(`INSERT INTO retry_probe VALUES(1)`); return e })
	if e != nil {
		t.Fatal("transient writer lock was not retried", e)
	}
	if e = <-released; e != nil {
		t.Fatal(e)
	}
	var n int
	if e = db.QueryRow(`SELECT COUNT(*) FROM retry_probe`).Scan(&n); e != nil || n != 1 {
		t.Fatal("retry duplicated write", n, e)
	}

	lock, e = second.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Rollback()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	e = WriteTx(ctx, db, func(tx *sql.Tx) error { _, e := tx.Exec(`INSERT INTO retry_probe VALUES(2)`); return e })
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("cancelled write retried past deadline", e)
	}
	if e = lock.Rollback(); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT COUNT(*) FROM retry_probe`).Scan(&n); e != nil || n != 1 {
		t.Fatal("cancelled write persisted", n, e)
	}
}

func TestWriteTxRollsBackWithoutRetryingBusinessErrors(t *testing.T) {
	db, e := Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`CREATE TABLE rollback_probe(id INTEGER PRIMARY KEY)`); e != nil {
		t.Fatal(e)
	}
	rejection := errors.New("business rejection")
	calls := 0
	e = WriteTx(context.Background(), db, func(tx *sql.Tx) error {
		calls++
		if _, e := tx.Exec(`INSERT INTO rollback_probe VALUES(1)`); e != nil {
			return e
		}
		return rejection
	})
	if !errors.Is(e, rejection) || calls != 1 {
		t.Fatal("business error was retried", calls, e)
	}
	var n int
	if e = db.QueryRow(`SELECT COUNT(*) FROM rollback_probe`).Scan(&n); e != nil || n != 0 {
		t.Fatal("failed transaction persisted", n, e)
	}
}

func TestWriteTxDefaultTimeoutDoesNotBlockCancelledRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")
	db, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	second, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	if _, e = db.Exec(`CREATE TABLE cancelled_probe(id INTEGER PRIMARY KEY)`); e != nil {
		t.Fatal(e)
	}
	lock, e := second.Begin()
	if e != nil {
		t.Fatal(e)
	}
	released := make(chan error, 1)
	go func() { time.Sleep(500 * time.Millisecond); released <- lock.Rollback() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	e = WriteTx(ctx, db, func(tx *sql.Tx) error { _, e := tx.ExecContext(ctx, `INSERT INTO cancelled_probe VALUES(1)`); return e })
	elapsed := time.Since(started)
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Errorf("unexpected cancellation result: %v", e)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("cancelled request blocked behind native busy handler: %s", elapsed)
	}
	if e = <-released; e != nil {
		t.Fatal(e)
	}
	var n int
	if e = db.QueryRow(`SELECT COUNT(*) FROM cancelled_probe`).Scan(&n); e != nil || n != 0 {
		t.Fatal("cancelled write persisted", n, e)
	}
}

func TestWriteTxCancellationRollsBackAcquiredTransaction(t *testing.T) {
	db, e := Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`CREATE TABLE in_flight_probe(id INTEGER PRIMARY KEY)`); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	e = WriteTx(ctx, db, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `INSERT INTO in_flight_probe VALUES(1)`); e != nil {
			return e
		}
		<-ctx.Done()
		return nil // Commit must check context even if the callback forgot to do so.
	})
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("cancelled transaction reported success", e)
	}
	if _, e = db.Exec(`INSERT INTO in_flight_probe VALUES(2)`); e != nil {
		t.Fatal("cancelled transaction leaked its writer lock", e)
	}
	var sum int
	if e = db.QueryRow(`SELECT SUM(id) FROM in_flight_probe`).Scan(&sum); e != nil || sum != 2 {
		t.Fatal("cancelled transaction committed", sum, e)
	}
}

func TestWriteTxRetriesCommitBusyWithoutDiscardingCleanConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")
	db, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	// Rollback journal lets a reader block COMMIT. WAL normally avoids this
	// conflict, but the retry helper must still handle SQLite's original BUSY.
	if _, e = db.Exec(`PRAGMA journal_mode=DELETE;CREATE TABLE commit_probe(id INTEGER PRIMARY KEY)`); e != nil {
		t.Fatal(e)
	}
	second, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	reader, e := second.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Rollback()
	var n int
	if e = reader.QueryRow(`SELECT COUNT(*) FROM commit_probe`).Scan(&n); e != nil {
		t.Fatal(e)
	}
	attempted := make(chan struct{})
	released := make(chan error, 1)
	go func() { <-attempted; time.Sleep(30 * time.Millisecond); released <- reader.Rollback() }()
	calls := 0
	e = WriteTx(context.Background(), db, func(tx *sql.Tx) error {
		calls++
		if _, e := tx.Exec(`INSERT INTO commit_probe VALUES(1)`); e != nil {
			return e
		}
		if calls == 1 {
			close(attempted)
		}
		return nil
	})
	if releaseErr := <-released; releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if e != nil {
		t.Fatal("COMMIT BUSY failed instead of retrying", e)
	}
	if calls < 2 {
		t.Fatal("fixture did not exercise COMMIT contention")
	}
	if e = db.QueryRow(`SELECT COUNT(*) FROM commit_probe`).Scan(&n); e != nil || n != 1 {
		t.Fatal("failed commit persisted duplicate answers", n, e)
	}
}
