package main

import (
	"ai_analyze_guokao/internal/setting"
	"ai_analyze_guokao/internal/store"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

type cliTransport func(*http.Request) (*http.Response, error)

func (f cliTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSingleModelsFlagControlsActualRequest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.sqlite")
	key := filepath.Join(dir, "key")
	db, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	s, e := setting.Open(db, key)
	if e != nil {
		t.Fatal(e)
	}
	for k, v := range map[string]string{setting.KeyBaseURL: "https://test.invalid/v1", setting.KeyAPIKey: "synthetic", setting.KeyModel: "configured-A"} {
		if e = s.Set(context.Background(), k, v, "test"); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.Exec(`INSERT INTO question(content_hash,module,stem,answer,answer_type) VALUES('test','资料分析','stem','A','single')`); e != nil {
		t.Fatal(e)
	}
	db.Close()
	var models []string
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	http.DefaultTransport = cliTransport(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		models = append(models, body["model"].(string))
		status := 200
		raw := `{"choices":[{"message":{"content":"ping"}}]}`
		if body["max_tokens"].(float64) > 8 {
			status = 400
			raw = `{"error":{"message":"test stop"}}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(raw)), Header: http.Header{}, Request: r}, nil
	})
	// An invalid label is intentional: model routing is observable before validation.
	_ = cmdDistillRun([]string{"--db", path, "--secret-key-file", key, "--taxonomy", "../../taxonomy/v1/taxonomy.yaml", "--run-dir", dir, "--run", "test", "--models", "requested-B", "--concurrency", "1"})
	if len(models) != 2 || models[0] != "requested-B" || models[1] != "requested-B" {
		t.Fatalf("requested B, actual requests=%v", models)
	}
}
