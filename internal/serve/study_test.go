package serve

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai_analyze_guokao/internal/setting"
	"ai_analyze_guokao/internal/store"
	"ai_analyze_guokao/internal/study"
)

func fixture(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	settings, err := setting.Open(db, filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range []struct {
		typ, answer string
		options     int
	}{{"single", "H", 8}, {"multi", "AC", 4}, {"judge", "B", 0}, {"other", "（缺）", 0}, {"single", "A", 4}} {
		id := i + 1
		_, err = db.Exec(`INSERT INTO question(id,content_hash,module,stem,answer,answer_type,option_count,explanation,explanation_with_formula) VALUES(?,?,'资料分析','增长率计算与增长量比较',?,?,?,'官方解释','$\frac{1}{2}$ 中文原文')`, id, fmt.Sprint(id), v.answer, v.typ, v.options)
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < v.options; j++ {
			_, err = db.Exec(`INSERT INTO option(question_id,ord,label,content,is_correct) VALUES(?,?,?,?,?)`, id, j+1, string(rune('A'+j)), fmt.Sprintf("选项 %d", j+1), j == 0)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, query := range []string{`INSERT INTO paper(id,name,module,year,region,exam_type,question_count,source_path) VALUES(1,'测试卷','资料分析',2026,'陕西','省考',5,'fixture')`, `INSERT INTO question_occurrence(question_id,paper_id,number) SELECT id,1,id FROM question`, `INSERT INTO label_run(id,status) VALUES('old','finished'),('new','finished')`, `INSERT INTO label(question_id,run_id,subject,secondary,tertiary,reasoning_chain,detail,fastest_solution) VALUES(1,'old','资料分析','增长','增长-增长率计算','["隐含正确答案 H"]','特征','选择 H'),(1,'new','资料分析','增长','增长-增长量计算','["隐含正确答案 H"]','特征','选择 H')`} {
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	s := New(settings, db, nil)
	if err = s.Study.RefreshConcepts(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, db
}
func requestTest(t *testing.T, h http.Handler, method, path, body string, cookie *http.Cookie) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var v map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return w, v
}
func account(t *testing.T, h http.Handler, name string) *http.Cookie {
	t.Helper()
	w, _ := requestTest(t, h, "POST", "/api/auth/register", fmt.Sprintf(`{"username":%q,"password":"testpassword"}`, name), nil)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w, _ = requestTest(t, h, "POST", "/api/auth/login", fmt.Sprintf(`{"username":%q,"password":"testpassword"}`, name), nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return w.Result().Cookies()[0]
}
func TestPracticeSecurityAndGrading(t *testing.T) {
	s, db := fixture(t)
	h := s.Routes()
	alice := account(t, h, "alice")
	bob := account(t, h, "bob")
	for _, path := range []string{"/api/questions?module=资料分析", "/api/questions/1"} {
		w, v := requestTest(t, h, "GET", path, "", alice)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		assertNoAnswers(t, v)
		if strings.Contains(w.Body.String(), "隐含正确答案") {
			t.Fatal("reasoning leak")
		}
	}
	w, _ := requestTest(t, h, "GET", "/api/questions/1/reveal", "", alice)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	w, v := requestTest(t, h, "POST", "/api/practice/sessions", `{"kind":"single","spec":{"question_ids":[1,2,3]}}`, alice)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	assertNoAnswers(t, v)
	id := int(v["session_id"].(float64))
	path := fmt.Sprintf("/api/practice/sessions/%d", id)
	w, _ = requestTest(t, h, "GET", path, "", bob)
	if w.Code != 404 {
		t.Fatal("other user can read session", w.Code)
	}
	w, _ = requestTest(t, h, "POST", path+"/submit", `{"draft_revision":0,"answers":[{"question_id":5,"answer":"A"}]}`, alice)
	if w.Code != 400 {
		t.Fatal("foreign question accepted", w.Code)
	}
	w, _ = requestTest(t, h, "POST", path+"/submit", `{"draft_revision":0,"answers":[{"question_id":1,"answer":"Z"}]}`, alice)
	if w.Code != 400 {
		t.Fatal("invalid option accepted", w.Code)
	}
	draft := `{"draft_revision":0,"answers":[{"question_id":1,"answer":"H","duration_ms":1200},{"question_id":2,"answer":"CA"},{"question_id":3,"answer":"B"}]}`
	w, _ = requestTest(t, h, "PUT", path+"/answers", draft, alice)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w, v = requestTest(t, h, "GET", path, "", alice)
	if w.Code != 200 || len(v["answers"].([]any)) != 3 {
		t.Fatal("draft recovery", w.Body.String())
	}
	draft = strings.Replace(draft, `"draft_revision":0`, `"draft_revision":1`, 1)
	w, v = requestTest(t, h, "POST", path+"/submit", draft, alice)
	if w.Code != 200 || v["correct"].(float64) != 3 {
		t.Fatal("grading", w.Body.String())
	}
	w, _ = requestTest(t, h, "GET", "/api/questions/1/reveal", "", alice)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `frac`) {
		t.Fatal("formula missing", w.Body.String())
	}
	w, _ = requestTest(t, h, "GET", "/api/questions/1/reveal", "", bob)
	if w.Code != 403 {
		t.Fatal("other user's answer grants reveal")
	}
	w, v = requestTest(t, h, "POST", path+"/submit", draft, alice)
	if w.Code != 200 || v["correct"].(float64) != 3 {
		t.Fatal("idempotent submit", w.Body.String())
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM practice_answer`).Scan(&n)
	if n != 3 {
		t.Fatal("duplicated answers", n)
	}
	w, _ = requestTest(t, h, "POST", "/api/practice/sessions", `{"kind":"single","spec":{"question_ids":[4]}}`, alice)
	if w.Code != 400 {
		t.Fatal("missing answer should be disabled")
	}
	// Answer type is public, correct answer is not. Judge options are synthesized.
	w, v = requestTest(t, h, "GET", "/api/questions/3", "", alice)
	if w.Code != 200 || v["options"].([]any)[0].(map[string]any)["content"] != "正确" {
		t.Fatal("judge mapping", w.Body.String())
	}
}
func assertNoAnswers(t *testing.T, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if k == "answer" || k == "explanation" || k == "explanation_with_formula" || k == "is_correct" || k == "reasoning_chain" || k == "fastest_solution" {
				t.Fatalf("answer field leaked: %s", k)
			}
			assertNoAnswers(t, val)
		}
	case []any:
		for _, val := range x {
			assertNoAnswers(t, val)
		}
	}
}
func TestWrongbookIsolationRetryAndSkippedReveal(t *testing.T) {
	s, db := fixture(t)
	h := s.Routes()
	alice := account(t, h, "alice")
	bob := account(t, h, "bob")
	create := func(ids string) int {
		w, v := requestTest(t, h, "POST", "/api/practice/sessions", `{"kind":"single","spec":{"question_ids":`+ids+`}}`, alice)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		return int(v["session_id"].(float64))
	}
	id := create("[1,2]")
	path := fmt.Sprintf("/api/practice/sessions/%d/submit", id)
	body := `{"draft_revision":0,"answers":[{"question_id":1,"answer":"A"}]}`
	for i := 0; i < 2; i++ {
		w, _ := requestTest(t, h, "POST", path, body, alice)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	var count, resolved int
	db.QueryRow(`SELECT wrong_count,resolved FROM wrongbook WHERE user_id=1 AND question_id=1`).Scan(&count, &resolved)
	if count != 1 || resolved != 0 {
		t.Fatal(count, resolved)
	}
	w, _ := requestTest(t, h, "GET", "/api/questions/2/reveal", "", alice)
	if w.Code != 403 {
		t.Fatal("skipped answer revealed", w.Code)
	}
	w, v := requestTest(t, h, "GET", "/api/wrongbook", "", bob)
	if w.Code != 200 || v["total"].(float64) != 0 {
		t.Fatal("wrongbook isolation")
	}
	id = create("[1]")
	w, _ = requestTest(t, h, "POST", fmt.Sprintf("/api/practice/sessions/%d/submit", id), `{"draft_revision":0,"answers":[{"question_id":1,"answer":"H"}]}`, alice)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	db.QueryRow(`SELECT resolved FROM wrongbook WHERE user_id=1 AND question_id=1`).Scan(&resolved)
	if resolved != 1 {
		t.Fatal("not resolved")
	}
}
func TestSearchConceptsAndOrigin(t *testing.T) {
	s, db := fixture(t)
	h := s.Routes()
	alice := account(t, h, "alice")
	for _, term := range []string{"增长率", "增长", "增长率\" OR *"} {
		w, _ := requestTest(t, h, "GET", "/api/questions?q="+url.QueryEscape(term), "", alice)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var n, quantity int
	db.QueryRow(`SELECT COUNT(*),SUM(question_count) FROM concept`).Scan(&n, &quantity)
	if n != 1 || quantity != 1 {
		t.Fatal("historical labels double counted", n, quantity)
	}
	r := httptest.NewRequest("POST", "/api/wrongbook/1", strings.NewReader(`{"action":"favorite"}`))
	r.Header.Set("Origin", "https://evil.example")
	r.AddCookie(alice)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin write accepted")
	}
	w, _ = requestTest(t, h, "GET", "/api/no-such-api", "", nil)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "error") {
		t.Fatal("api should not return SPA")
	}
}
func TestPasswordRevokesSessions(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	cookie := account(t, h, "alice")
	w, _ := requestTest(t, h, "POST", "/api/auth/password", `{"current_password":"testpassword","password":"newpassword"}`, cookie)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w, _ = requestTest(t, h, "GET", "/api/auth/me", "", cookie)
	if w.Code != 401 {
		t.Fatal("old session survives password change")
	}
}
func TestGradeShapes(t *testing.T) {
	for _, v := range []struct {
		kind, official, input string
		want                  bool
	}{{"single", "H", "h", true}, {"multi", "AC", "C,A", true}, {"multi", "AC", "ABC", false}, {"judge", "B", "B", true}, {"judge", "A", "B", false}, {"single", "A", "", false}} {
		if got := study.Grade(v.kind, v.official, v.input); got != v.want {
			t.Fatal(v, got)
		}
	}
}

func TestMediaAuthenticationAndTraversal(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	cookie := account(t, h, "alice")
	s.DataDir = t.TempDir()
	root := filepath.Join(s.DataDir, "90-图片", "题目图")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "diagram.png"), []byte("test-image"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(s.DataDir, "secret.png")
	os.WriteFile(outside, []byte("outside"), 0600)
	os.Symlink(outside, filepath.Join(root, "escape.png"))
	for _, v := range []struct {
		path   string
		cookie *http.Cookie
		code   int
	}{{"/media/题目图/diagram.png", nil, 401}, {"/media/题目图/diagram.png", cookie, 200}, {"/media/题目图/escape.png", cookie, 404}, {"/media/题目图/../secret.png", cookie, 404}, {"/media/源笔记/notes.md", cookie, 404}} {
		w, _ := requestTest(t, h, "GET", v.path, "", v.cookie)
		if w.Code != v.code {
			t.Fatal(v.path, w.Code, w.Body.String())
		}
	}
}

func TestConceptSearchDoesNotMatchUnlabeledStems(t *testing.T) {
	s, _ := fixture(t)
	keyword, err := s.Study.Search(context.Background(), study.Filter{Q: "增长量"}, "keyword")
	if err != nil || len(keyword) != 5 {
		t.Fatal("keyword matches", len(keyword), err)
	}
	concept, err := s.Study.Search(context.Background(), study.Filter{Q: "增长量"}, "concept")
	if err != nil || len(concept) != 1 {
		t.Fatal("concept matches", len(concept), err)
	}
}
