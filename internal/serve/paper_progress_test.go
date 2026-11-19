package serve

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestPaperProgressOwnershipAndResumePriority(t *testing.T) {
	s, db := fixture(t)
	h := s.Routes()
	alice, bob := account(t, s, "paper_alice"), account(t, s, "paper_bob")
	w, _ := requestTest(t, h, "GET", "/api/papers/progress?paper_ids=1", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", w.Code)
	}
	create := func() int {
		t.Helper()
		w, v := requestTest(t, h, "POST", "/api/practice/sessions", `{"kind":"paperset","spec":{"paper_id":1}}`, alice)
		if w.Code != 200 {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		return int(v["session_id"].(float64))
	}
	old, latest, completed := create(), create(), create()
	w, _ = requestTest(t, h, "POST", fmt.Sprintf("/api/practice/sessions/%d/submit", completed), `{"draft_revision":0,"answers":[{"question_id":1,"answer":"H"}]}`, alice)
	if w.Code != 200 {
		t.Fatalf("submit: %d %s", w.Code, w.Body.String())
	}
	w, _ = requestTest(t, h, "PUT", fmt.Sprintf("/api/practice/sessions/%d/answers", latest), `{"draft_revision":0,"answers":[{"question_id":1,"answer":"H"}]}`, alice)
	if w.Code != 200 {
		t.Fatalf("draft: %d %s", w.Code, w.Body.String())
	}
	read := func(cookie *http.Cookie) []any {
		t.Helper()
		var sessionsBefore, quotaBefore int
		if err := db.QueryRow(`SELECT COUNT(*) FROM practice_session`).Scan(&sessionsBefore); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT COALESCE(SUM(papers),0) FROM daily_usage`).Scan(&quotaBefore); err != nil {
			t.Fatal(err)
		}
		w, v := requestTest(t, h, "GET", "/api/papers/progress?paper_ids=1,1,999999", "", cookie)
		if w.Code != 200 {
			t.Fatalf("progress: %d %s", w.Code, w.Body.String())
		}
		for _, forbidden := range []string{`"answers"`, `"question_ids"`, `"correct_answer"`, `"explanation"`, `"spec"`, `"draft"`} {
			if strings.Contains(w.Body.String(), forbidden) {
				t.Fatalf("progress leaked %s", forbidden)
			}
		}
		var sessionsAfter, quotaAfter int
		if err := db.QueryRow(`SELECT COUNT(*) FROM practice_session`).Scan(&sessionsAfter); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT COALESCE(SUM(papers),0) FROM daily_usage`).Scan(&quotaAfter); err != nil {
			t.Fatal(err)
		}
		if sessionsAfter != sessionsBefore || quotaAfter != quotaBefore {
			t.Fatal("reading progress created a session or consumed quota")
		}
		items, ok := v["items"].([]any)
		if !ok {
			t.Fatalf("items missing: %s", w.Body.String())
		}
		return items
	}
	if got := read(bob); len(got) != 0 {
		t.Fatal("other user's practice leaked", got)
	}
	got := read(alice)
	if len(got) != 1 {
		t.Fatalf("wanted one paper, got %d", len(got))
	}
	item := got[0].(map[string]any)
	if item["paper_id"] != float64(1) || item["session_id"] != float64(latest) || item["status"] != "in_progress" || item["answered"] != float64(1) || item["total"] != float64(4) {
		t.Fatalf("unexpected saved progress: %v", item)
	}
	w, _ = requestTest(t, h, "POST", fmt.Sprintf("/api/practice/sessions/%d/submit", latest), `{"draft_revision":1,"answers":[{"question_id":1,"answer":"H"}]}`, alice)
	if w.Code != 200 {
		t.Fatalf("submit latest: %d", w.Code)
	}
	item = read(alice)[0].(map[string]any)
	if item["session_id"] != float64(old) || item["answered"] != float64(0) {
		t.Fatalf("older unfinished paper not resumable: %v", item)
	}
	w, _ = requestTest(t, h, "POST", fmt.Sprintf("/api/practice/sessions/%d/submit", old), `{"draft_revision":0,"answers":[]}`, alice)
	if w.Code != 200 {
		t.Fatalf("submit oldest: %d", w.Code)
	}
	// Creation order differs from submission order, including mixed UTC offsets.
	if _, err := db.Exec(`UPDATE practice_session SET submitted_at=CASE id WHEN ? THEN '2026-11-19T10:03:00Z' WHEN ? THEN '2026-11-19T18:01:00+08:00' ELSE '2026-11-19T09:00:00Z' END WHERE user_id=(SELECT id FROM app_user WHERE username='paper_alice')`, old, latest); err != nil {
		t.Fatal(err)
	}
	item = read(alice)[0].(map[string]any)
	if item["session_id"] != float64(old) || item["status"] != "completed" || item["answered"] != float64(0) || item["correct"] != float64(0) {
		t.Fatalf("latest submitted result not linked: %v", item)
	}
}

func TestPaperProgressValidatesBatchAndIdentity(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	cookie := account(t, s, "paper_validation")
	for _, query := range []string{"", "paper_ids=", "paper_ids=0", "paper_ids=-1", "paper_ids=1,x", "paper_ids=1,,2", "paper_ids=" + strings.Repeat("1,", 100) + "1"} {
		w, _ := requestTest(t, h, "GET", "/api/papers/progress?"+query, "", cookie)
		if w.Code != 400 {
			t.Errorf("bad batch %q: %d", query, w.Code)
		}
	}
	r := fmt.Sprintf("/api/papers/progress?paper_ids=1&user_id=%d", 99999)
	w, v := requestTest(t, h, "GET", r, "", cookie)
	if w.Code != 200 || len(v["items"].([]any)) != 0 {
		t.Fatal("query user override accepted", w.Code)
	}
}
