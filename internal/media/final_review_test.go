package media

import (
	"ai_analyze_guokao/internal/store"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFinalOCRCancelKeepsAccounting(t *testing.T) {
	d := t.TempDir()
	db, e := store.Open(filepath.Join(d, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`INSERT INTO image(url,kind,name,sha256) VALUES('a','公式图','a','a'),('b','公式图','b','b'),('c','公式图','c','c')`); e != nil {
		t.Fatal(e)
	}
	script := filepath.Join(d, "worker.sh")
	body := `while [ "$#" -gt 0 ]; do
if [ "$1" = "--out" ]; then shift; out="$1"; fi
shift
done
case "$out" in
*result-001*) printf '%s\n' '{"sha256":"a","status":"ok","tex":"ok"}' > "$out" ;;
*) while :; do :; done ;;
esac
`
	if e = os.WriteFile(script, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	st, e := RunOCR(ctx, db, OCROptions{Python: "/bin/sh", Script: script, DataDir: d, RunDir: d, ShardSize: 1})
	t.Logf("stats=%+v err=%v", st, e)
	if e == nil || st == nil || st.Done+st.Failed+st.Missing != st.Unique {
		t.Fatal("cancellation loses outstanding image accounting")
	}
}
func TestFinalOCRDiskErrorKeepsPartialStats(t *testing.T) {
	d := t.TempDir()
	db, e := store.Open(filepath.Join(d, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`INSERT INTO image(url,kind,name,sha256) VALUES('a','公式图','a','a'),('b','公式图','b','b')`); e != nil {
		t.Fatal(e)
	}
	script := filepath.Join(d, "worker.sh")
	body := `while [ "$#" -gt 0 ]; do
if [ "$1" = "--out" ]; then shift; out="$1"; fi
shift
done
printf '%s\n' '{"sha256":"a","status":"ok","tex":"ok"}' > "$out"
mkdir "${out%/*}/manifest-002.jsonl"
`
	if e = os.WriteFile(script, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	st, e := RunOCR(context.Background(), db, OCROptions{Python: "/bin/sh", Script: script, DataDir: d, RunDir: d, ShardSize: 1})
	var ok int
	db.QueryRow(`SELECT COUNT(*) FROM image WHERE ocr_status='ok'`).Scan(&ok)
	t.Logf("stats=%+v err=%v persisted_ok=%d", st, e, ok)
	if e == nil || st == nil || st.Done != ok {
		t.Fatal("disk error drops successful shard stats")
	}
}
