package distill

import (
	"ai_analyze_guokao/internal/llm"
	"ai_analyze_guokao/internal/media"
	"ai_analyze_guokao/internal/store"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestActualModelRequestIncludesMaterialAndOptionOCR(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, sql := range []string{
		`INSERT INTO paper(id,name,source_path) VALUES(1,'paper','source')`,
		`INSERT INTO material(id,paper_id,seq,body,body_with_text) VALUES(1,1,1,'raw material','2013 material OCR 644.55 and 2.92%')`,
		`INSERT INTO question(id,content_hash,module,stem,answer,answer_type) VALUES(1,'input','资料分析','calculate total','A','single')`,
		`INSERT INTO question_occurrence(question_id,paper_id,material_id,number,raw_tag) VALUES(1,1,1,1,'增长')`,
		`INSERT INTO option(question_id,ord,label,content) VALUES(1,1,'A','⟦IMG:公式图/a.png⟧')`,
		`INSERT INTO image(url,kind,name,ocr_status,ocr_engine,ocr_tex) VALUES('公式图/a.png','公式图','a.png','ok','formula','\\frac{644.55}{0.0292}')`,
	} {
		if _, e = db.Exec(sql); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = media.Backfill(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	qs, e := store.LoadDistillQuestions(context.Background(), db, "", 0)
	if e != nil {
		t.Fatal(e)
	}
	var request string
	c := llm.New("https://test.invalid", "", 0)
	c.HTTP.Transport = testTransport(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		request = string(b)
		return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"test stop"}}`)), Header: http.Header{}, Request: r}, nil
	})
	r := New(db, c, &Taxonomy{Version: "test"}, Options{RunID: "input", RunDir: t.TempDir(), Model: "test", Concurrency: 1})
	if _, e = r.RunOn(context.Background(), qs, true); e == nil {
		t.Fatal("simulated model error not propagated")
	}
	if !strings.Contains(request, "2013 material OCR 644.55 and 2.92%") {
		t.Fatal("material missing from actual request")
	}
	if !strings.Contains(request, "frac") || strings.Contains(request, "IMG:公式图/a.png") {
		t.Fatal("option OCR missing from actual request")
	}
}

func TestResumeRejectsOldInputVersion(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`INSERT INTO label_run(id,status,prompt_version,taxonomy_version) VALUES('old','running','v1','test')`); e != nil {
		t.Fatal(e)
	}
	r := New(db, nil, &Taxonomy{Version: "test"}, Options{RunID: "old", RunDir: t.TempDir()})
	if _, e = r.RunOn(context.Background(), nil, true); e == nil {
		t.Fatal("old input version accepted")
	}
}
