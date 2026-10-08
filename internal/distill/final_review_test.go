package distill

import (
	"ai_analyze_guokao/internal/llm"
	"ai_analyze_guokao/internal/store"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFinalMissingUsage(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, e = db.Exec(`INSERT INTO question(id,content_hash,module,stem,answer,answer_type) VALUES(1,'test','资料分析','stem','A','single')`)
	if e != nil {
		t.Fatal(e)
	}
	c := llm.New("https://test.invalid", "", 0)
	c.HTTP.Transport = testTransport(func(req *http.Request) (*http.Response, error) {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": validTestLabel}}}, "model": "m"})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b))), Request: req}, nil
	})
	r := New(db, c, testTax(), Options{RunID: "missing", RunDir: t.TempDir(), Model: "m", Concurrency: 1, HasPricing: true, Pricing: llm.Pricing{InputPer1K: 1, OutputPer1K: 2}})
	if _, e = r.RunOn(context.Background(), []store.DistillQuestion{{ID: 1, Module: "资料分析", Answer: "A", Stem: "stem"}}, true); e != nil {
		t.Fatal(e)
	}
	var complete, known, tokens int
	var cost float64
	if e = db.QueryRow(`SELECT usage_complete,cost_known,tokens_in,cost_usd FROM label_run WHERE id='missing'`).Scan(&complete, &known, &tokens, &cost); e != nil {
		t.Fatal(e)
	}
	t.Logf("complete=%d known=%d tokens=%d cost=%v", complete, known, tokens, cost)
	if complete != 0 || known != 0 {
		t.Fatal("missing usage falsely complete/known")
	}
}
func TestFinalConcurrentResume(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, e = db.Exec(`INSERT INTO question(id,content_hash,module,stem,answer,answer_type) VALUES(1,'test','资料分析','stem','A','single')`)
	if e != nil {
		t.Fatal(e)
	}
	qs := []store.DistillQuestion{{ID: 1, Module: "资料分析", Answer: "A", Stem: "stem"}}
	opt := Options{RunID: "concurrent", RunDir: t.TempDir(), Model: "m", Concurrency: 1}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	c := llm.New("https://test.invalid", "", 0)
	c.HTTP.Transport = testTransport(func(req *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		<-release
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": validTestLabel}}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}, "model": "m"})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b))), Request: req}, nil
	})
	second := opt
	second.RunDir = t.TempDir()
	go func() { _, err := New(db, c, testTax(), second).RunOn(context.Background(), qs, true); results <- err }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first did not call")
	}
	go func() { _, err := New(db, c, testTax(), opt).RunOn(context.Background(), qs, true); results <- err }()
	duplicate := false
	select {
	case <-entered:
		duplicate = true
	case e := <-results:
		t.Logf("second refused: %v", e)
	case <-time.After(10 * time.Second):
		t.Fatal("second stuck")
	}
	close(release)
	t.Logf("first completion: %v", <-results)
	if duplicate {
		t.Logf("second completion: %v", <-results)
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM label_call`).Scan(&n)
		t.Fatalf("concurrent resume paid twice for same question; calls=%d", n)
	}
}
func TestFinalPaidNoChoices(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, e = db.Exec(`INSERT INTO question(id,content_hash,module,stem,answer,answer_type) VALUES(1,'test','资料分析','stem','A','single')`)
	if e != nil {
		t.Fatal(e)
	}
	c := llm.New("https://test.invalid", "", 0)
	calls := 0
	c.HTTP.Transport = testTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		choices := []any{}
		if calls > 1 {
			choices = append(choices, map[string]any{"message": map[string]string{"content": validTestLabel}})
		}
		b, _ := json.Marshal(map[string]any{"choices": choices, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}, "model": "m"})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b))), Request: req}, nil
	})
	r := New(db, c, testTax(), Options{RunID: "nochoices", RunDir: t.TempDir(), Model: "m", Concurrency: 1, MaxRetries: 1})
	if _, e = r.RunOn(context.Background(), []store.DistillQuestion{{ID: 1, Module: "资料分析", Answer: "A", Stem: "stem"}}, true); e != nil {
		t.Fatal(e)
	}
	var tin, n int
	db.QueryRow(`SELECT tokens_in FROM label_run`).Scan(&tin)
	db.QueryRow(`SELECT COUNT(*) FROM label_call`).Scan(&n)
	if tin != 20 || n != 2 {
		t.Fatalf("paid usage discarded: requests=%d recorded_calls=%d tokens_in=%d", calls, n, tin)
	}
}
