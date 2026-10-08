package media

import (
	"ai_analyze_guokao/internal/store"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOCRWorkerFailureCannotReportSuccess(t *testing.T) {
	dir := t.TempDir()
	db, e := store.Open(filepath.Join(dir, "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`INSERT INTO image(url,kind,name,sha256) VALUES('公式图/a.png','公式图','a.png','test-sha')`); e != nil {
		t.Fatal(e)
	}
	script := filepath.Join(dir, "fail.sh")
	if e = os.WriteFile(script, []byte("#!/bin/sh\nexit 7\n"), 0600); e != nil {
		t.Fatal(e)
	}
	st, e := RunOCR(context.Background(), db, OCROptions{Python: "/bin/sh", Script: script, DataDir: dir, RunDir: dir, Batch: 1})
	if e == nil {
		t.Fatal("failed worker reported successful run")
	}
	if st == nil || st.Missing != 1 {
		t.Fatalf("missing work unaccounted: %+v", st)
	}
}

func TestOCRRejectsResultsOutsideManifest(t *testing.T) {
	dir := t.TempDir()
	db, e := store.Open(filepath.Join(dir, "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`INSERT INTO image(url,kind,name,sha256,ocr_status,ocr_tex) VALUES('公式图/a.png','公式图','a.png','expected',NULL,NULL),('公式图/b.png','公式图','b.png','other','ok','preserve')`); e != nil {
		t.Fatal(e)
	}
	script := filepath.Join(dir, "wrong.sh")
	body := "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n if [ \"$1\" = \"--out\" ]; then shift; out=\"$1\"; fi\n shift\ndone\nprintf '%s\\n' '{\"sha256\":\"other\",\"status\":\"ok\",\"tex\":\"wrong\"}' > \"$out\"\n"
	if e = os.WriteFile(script, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = RunOCR(context.Background(), db, OCROptions{Python: "/bin/sh", Script: script, DataDir: dir, RunDir: dir, Batch: 1}); e == nil {
		t.Fatal("out-of-manifest result accepted")
	}
	var text string
	if e = db.QueryRow(`SELECT ocr_tex FROM image WHERE sha256='other'`).Scan(&text); e != nil || text != "preserve" {
		t.Fatal("unrelated image overwritten", e)
	}
}
