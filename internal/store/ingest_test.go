package store

import (
	"ai_analyze_guokao/internal/ingest"
	"ai_analyze_guokao/internal/model"
	"path/filepath"
	"testing"
)

func TestIngestRepeatKeepsPaperAndMaterialOwnership(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	g, err := NewIngester(db, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	a := &ingest.File{Paper: model.Paper{Name: "A", Module: "资料分析", SourcePath: "a.md"}, Questions: []model.Question{{Number: 9, Stem: "A", Answer: "A", AnswerType: model.AnswerSingle}}}
	b := &ingest.File{Paper: model.Paper{Name: "B", Module: "资料分析", SourcePath: "b.md"}, Questions: []model.Question{{Number: 1, Stem: "B", Answer: "B", AnswerType: model.AnswerSingle}}}
	for _, f := range []*ingest.File{a, b, a} {
		if _, _, err = g.File(f); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM question_occurrence o JOIN paper p ON p.id=o.paper_id JOIN question q ON q.id=o.question_id WHERE p.source_path='b.md' AND q.stem='A'`).Scan(&n)
	if n != 0 {
		t.Fatalf("A attached to B: %d", n)
	}
	m := &ingest.File{Paper: model.Paper{Name: "M", Module: "资料分析", SourcePath: "m.md"}, Materials: []model.Material{{Seq: 1, Body: "material"}}, Questions: []model.Question{{Number: 1, MaterialSeq: 1, Stem: "M", Answer: "A", AnswerType: model.AnswerSingle}}}
	for i := 0; i < 2; i++ {
		if _, _, err = g.File(m); err != nil {
			t.Fatal(err)
		}
	}
	db.QueryRow(`SELECT COUNT(*) FROM material`).Scan(&n)
	if n != 1 {
		t.Fatalf("duplicate materials: %d", n)
	}
	db.QueryRow(`SELECT COUNT(*) FROM question_occurrence`).Scan(&n)
	if n != 3 {
		t.Fatalf("duplicate occurrences: %d", n)
	}
	// Changed content at an existing source/number must not silently keep an old mapping.
	m.Questions[0].Stem = "changed"
	if _, _, err = g.File(m); err == nil {
		t.Fatal("conflicting source accepted")
	}
	db.QueryRow(`SELECT COUNT(*) FROM question WHERE stem='changed'`).Scan(&n)
	if n != 0 {
		t.Fatal("conflicting file not rolled back")
	}
}
