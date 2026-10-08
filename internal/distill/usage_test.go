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
)

const validTestLabel = `{"subject":"资料分析","secondary":"增长","tertiary":"增长-增长率计算","detail":"test","question_model":"test","reasoning_chain":["1","2","3"],"fastest_solution":"test","template":"test","key_features":["test"]}`

func testTax() *Taxonomy {
	return &Taxonomy{Version: "test", Modules: []Module{{Subject: "资料分析", Secondary: []Secondary{{Name: "增长", Tertiary: []string{"增长-增长率计算"}}}}}}
}
func TestResumePreservesCumulativeUsage(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, q := range []string{`INSERT INTO question(id,content_hash,module,stem,answer,answer_type) VALUES(1,'test','资料分析','stem','A','single')`, `INSERT INTO label_run(id,status,total,ok,tokens_in,tokens_out,cost_usd,prompt_version,taxonomy_version,model_config,base_url) VALUES('resume','finished',1,1,100,50,1.5,'v2','test','m','')`, `INSERT INTO label(question_id,run_id,tokens_in,tokens_out) VALUES(1,'resume',100,50)`} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	r := New(db, nil, testTax(), Options{RunID: "resume", RunDir: t.TempDir(), Model: "m", Concurrency: 1})
	if _, e = r.RunOn(context.Background(), []store.DistillQuestion{{ID: 1, Module: "资料分析"}}, true); e != nil {
		t.Fatal(e)
	}
	var ok, tin int
	var cost float64
	if e = db.QueryRow(`SELECT ok,tokens_in,cost_usd FROM label_run WHERE id='resume'`).Scan(&ok, &tin, &cost); e != nil {
		t.Fatal(e)
	}
	if ok != 1 || tin != 100 || cost != 1.5 {
		t.Fatalf("history overwritten: ok=%d tokens=%d cost=%v", ok, tin, cost)
	}
}

func TestUsageIncludesPaidInvalidResponse(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`INSERT INTO question(id,content_hash,module,stem,answer,answer_type) VALUES(1,'test','资料分析','stem','A','single')`); e != nil {
		t.Fatal(e)
	}
	c := llm.New("https://test.invalid", "", 0)
	attempt := 0
	c.HTTP.Transport = testTransport(func(r *http.Request) (*http.Response, error) {
		attempt++
		content := "{"
		if attempt > 1 {
			content = validTestLabel
		}
		body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}, "model": "m"})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
	})
	r := New(db, c, testTax(), Options{RunID: "paid", RunDir: t.TempDir(), Model: "m", Concurrency: 1, MaxRetries: 1, HasPricing: true, Pricing: llm.Pricing{InputPer1K: 1, OutputPer1K: 2}})
	if _, e = r.RunOn(context.Background(), []store.DistillQuestion{{ID: 1, Module: "资料分析", Answer: "A", Stem: "stem"}}, true); e != nil {
		t.Fatal(e)
	}
	var tin, tout int
	var cost float64
	if e = db.QueryRow(`SELECT tokens_in,tokens_out,cost_usd FROM label_run WHERE id='paid'`).Scan(&tin, &tout, &cost); e != nil {
		t.Fatal(e)
	}
	if tin != 20 || tout != 10 || cost < 0.0399 || cost > 0.0401 {
		t.Fatalf("lost retry usage: %d/%d cost=%v", tin, tout, cost)
	}
}

func TestResumeRejectsChangedQuestionSet(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	opt := Options{RunID: "frozen", Model: "m"}
	qs := []store.DistillQuestion{{ID: 1, Stem: "original"}}
	if e = prepareStoredRun(context.Background(), db, opt, testTax(), qs, "{}"); e != nil {
		t.Fatal(e)
	}
	qs[0].Stem = "changed"
	if e = prepareStoredRun(context.Background(), db, opt, testTax(), qs, "{}"); e == nil {
		t.Fatal("changed input accepted")
	}
}
