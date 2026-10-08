package store

import (
	"path/filepath"
	"testing"
)

func TestFinalMigrationPreservesVisibleBaseline(t *testing.T) {
	all := migrations
	defer func() { migrations = all }()
	migrations = migrations[:8]
	path := filepath.Join(t.TempDir(), "db")
	db, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{`INSERT INTO question(id,content_hash,module,stem,answer,answer_type) VALUES(1,'test','资料分析','stem','A','single')`, `INSERT INTO label_run(id,status,total,ok,tokens_in,tokens_out,cost_usd,prompt_version,taxonomy_version,model_config,base_url) VALUES('old','finished',1,0,0,0,0,'v1','test','m','')`, `INSERT INTO label(question_id,run_id,tokens_in,tokens_out) VALUES(1,'old',100,50)`} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	db.Close()
	migrations = all
	db, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var tokens, legacy, ok int
	if e = db.QueryRow(`SELECT tokens_in,legacy_tokens_in,ok FROM label_run WHERE id='old'`).Scan(&tokens, &legacy, &ok); e != nil {
		t.Fatal(e)
	}
	t.Logf("visible tokens=%d legacy=%d visible ok=%d", tokens, legacy, ok)
	if tokens != 100 || ok != 1 {
		t.Fatal("recoverable historical evidence absent from report fields")
	}
}
