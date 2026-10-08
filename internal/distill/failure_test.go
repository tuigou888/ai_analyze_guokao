package distill

import (
	"ai_analyze_guokao/internal/llm"
	"ai_analyze_guokao/internal/store"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMultiBucketFilesystemFailureReturnsError(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	dir := t.TempDir()
	p := filepath.Join(dir, "run", "engines")
	if e = os.MkdirAll(p, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(p, "m"), []byte("block"), 0600); e != nil {
		t.Fatal(e)
	}
	_, e = RunMulti(context.Background(), db, []Engine{{Model: "m", Concurrency: 1}}, Options{RunID: "run", RunDir: dir, Concurrency: 1}, []store.DistillQuestion{{ID: 1, Module: "资料分析"}}, testTax())
	if e == nil {
		t.Fatal("all bucket errors swallowed")
	}
	var status string
	db.QueryRow(`SELECT status FROM label_run WHERE id='run'`).Scan(&status)
	if status == "finished" {
		t.Fatal("failed bucket marked finished")
	}
}

func TestRunnerCancellationDoesNotBlockPendingJobs(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := llm.New("https://test.invalid", "", 0)
	c.HTTP.Transport = testTransport(func(r *http.Request) (*http.Response, error) { cancel(); return nil, context.Canceled })
	r := New(db, c, testTax(), Options{RunID: "cancel", RunDir: t.TempDir(), Model: "m", Concurrency: 1, MaxRetries: 1})
	done := make(chan error, 1)
	go func() { _, e := r.RunOn(ctx, []store.DistillQuestion{{ID: 1}, {ID: 2}, {ID: 3}}, true); done <- e }()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal("cancellation not propagated", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled worker left sender blocked")
	}
}
