package serve

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ai_analyze_guokao/internal/study"
)

func dashboardHTTP(t *testing.T, server *httptest.Server, method, path string, cookie *http.Cookie) (int, []byte, http.Header) {
	t.Helper()
	r, err := http.NewRequest(method, server.URL+path, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", server.URL)
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	resp, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body, resp.Header
}

func historySession(t *testing.T, db *sql.DB, user int64, submitted any, duration int, answers []string, correct []int) int64 {
	t.Helper()
	result, err := db.Exec(`INSERT INTO practice_session(user_id,kind,spec,question_ids,started_at,submitted_at,total,duration_ms) VALUES(?,'single','{}','[1,2,5]','2026-10-01T00:00:00Z',?,?,?)`, user, submitted, len(answers), duration)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	for i, answer := range answers {
		q := []int{1, 2, 5}[i%3]
		_, err = db.Exec(`INSERT INTO practice_answer(session_id,question_id,ord,user_answer,is_correct,duration_ms,answered_at) VALUES(?,?,?,?,?,100,'2026-10-01T00:00:00Z')`, id, q, i+1, answer, correct[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// Catches missing routing, UTC-day grouping, answer joins multiplying time,
// empty answer inclusion, draft inclusion, and missing user predicates.
func TestPersonalDashboardHTTPFixedClock(t *testing.T) {
	s, db := fixture(t)
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC) }
	h := s.Routes()
	alice, bob := account(t, h, "dashboard_alice"), account(t, h, "dashboard_bob")
	historySession(t, db, 1, "2026-10-03T15:59:59Z", 1000, []string{"H", "B", ""}, []int{1, 0, 1})
	historySession(t, db, 1, "2026-10-03T16:00:00Z", 2000, []string{"H", "AC"}, []int{1, 1})
	historySession(t, db, 1, "2026-09-27T16:00:00Z", 3000, []string{"B"}, []int{0})
	historySession(t, db, 1, "2026-09-27T15:59:59Z", 4000, []string{"H"}, []int{1})
	historySession(t, db, 1, nil, 50000, []string{"H"}, []int{1})
	historySession(t, db, 2, "2026-10-03T16:00:00Z", 9000, []string{"H"}, []int{1})
	ts := httptest.NewServer(h)
	defer ts.Close()
	status, body, _ := dashboardHTTP(t, ts, "GET", "/api/account/dashboard?days=7", alice)
	if status != 200 {
		t.Fatalf("dashboard route: want 200 got %d: %s", status, body)
	}
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	if v["timezone"] != "Asia/Shanghai" || v["start_date"] != "2026-09-28" || v["end_date"] != "2026-10-04" {
		t.Fatal(v)
	}
	for _, want := range []struct {
		key                                             string
		answered, correct, duration, sessions, accuracy float64
	}{{"period", 5, 3, 6000, 3, 60}, {"previous", 1, 1, 4000, 1, 100}, {"today", 2, 2, 2000, 1, 100}, {"lifetime", 6, 4, 10000, 4, 100.0 * 4 / 6}} {
		got := v[want.key].(map[string]any)
		if got["answered"] != want.answered || got["correct"] != want.correct || got["duration_ms"] != want.duration || got["sessions"] != want.sessions || got["accuracy"] != want.accuracy {
			t.Fatalf("%s: %v", want.key, got)
		}
	}
	trend := v["trend"].([]any)
	if len(trend) != 7 || trend[1].(map[string]any)["accuracy"] != nil {
		t.Fatal(trend)
	}
	if len(v["activity"].([]any)) != 90 {
		t.Fatal("activity range")
	}
	modules := v["modules"].([]any)
	if len(modules) != 6 || modules[0].(map[string]any)["module"] != "政治理论" || modules[0].(map[string]any)["accuracy"] != nil {
		t.Fatal(modules)
	}
	review := v["review"].(map[string]any)
	if review["labeled_answered"] != float64(3) || review["label_coverage"] != float64(60) {
		t.Fatal(review)
	}
	streak := v["streak"].(map[string]any)
	if streak["current"] != float64(2) || streak["longest"] != float64(2) || streak["practiced_today"] != true {
		t.Fatal(streak)
	}
	// Log a real response sample without repeating all 90 empty heatmap dates.
	v["activity"] = v["activity"].([]any)[86:]
	sample, _ := json.Marshal(v)
	t.Logf("fixed clock dashboard JSON sample (activity last 4): %s", sample)
	status, body, _ = dashboardHTTP(t, ts, "GET", "/api/account/dashboard?days=7", bob)
	json.Unmarshal(body, &v)
	if status != 200 || v["period"].(map[string]any)["answered"] != float64(1) {
		t.Fatal(status, string(body))
	}
	for _, days := range []int{30, 90} {
		status, body, _ = dashboardHTTP(t, ts, "GET", fmt.Sprintf("/api/account/dashboard?days=%d", days), alice)
		json.Unmarshal(body, &v)
		if status != 200 || len(v["trend"].([]any)) != days || v["period"].(map[string]any)["answered"] != float64(6) {
			t.Fatal(status, string(body))
		}
	}
	for _, days := range []string{"0", "8", "all", "abc"} {
		status, _, _ = dashboardHTTP(t, ts, "GET", "/api/account/dashboard?days="+days, alice)
		if status != 400 {
			t.Fatal(days, status)
		}
	}
	status, _, _ = dashboardHTTP(t, ts, "GET", "/api/account/dashboard", nil)
	if status != 401 {
		t.Fatal(status)
	}
}

// Catches stale user_concept_stat use and presenting an unmaterialized concept
// as a playable recommendation, while still accounting for latest-label coverage.
func TestPersonalDashboardLatestLabelsAndResume(t *testing.T) {
	s, db := fixture(t)
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC) }
	h := s.Routes()
	account(t, h, "labels_alice")
	account(t, h, "labels_bob")
	for i := 0; i < 5; i++ {
		historySession(t, db, 1, "2026-10-03T16:00:00Z", 100, []string{"H", "A"}, []int{1, 0})
	}
	for _, q := range []string{
		`INSERT INTO user_concept_stat SELECT 1,id,999,0,0,'2026-10-04T00:00:00Z' FROM concept`,
		`INSERT INTO label_run(id,status) VALUES('latest','finished')`,
		`INSERT INTO label(question_id,run_id,subject,secondary,tertiary) VALUES(1,'latest','资料分析','增长','最新考点')`,
		`INSERT INTO favorite VALUES(1,1,'2026-10-04T00:00:00Z'),(2,2,'2026-10-04T00:00:00Z')`,
		`INSERT INTO wrongbook VALUES(1,1,'auto',3,'2026-10-04T00:00:00Z',0),(1,2,'auto',9,'2026-10-04T00:00:00Z',1),(2,3,'auto',99,'2026-10-04T00:00:00Z',0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		id := historySession(t, db, 1, nil, 0, nil, nil)
		if _, err := db.Exec(`UPDATE practice_session SET started_at=?,draft='[{"question_id":1,"answer":"H"},{"question_id":2,"answer":""}]',draft_revision=3 WHERE id=?`, fmt.Sprintf("2026-10-0%dT00:00:00Z", i+1), id); err != nil {
			t.Fatal(err)
		}
	}
	historySession(t, db, 2, nil, 0, nil, nil)
	dashboard, err := s.Study.Dashboard(context.Background(), 1, 7)
	if err != nil {
		t.Fatal(err)
	}
	review := dashboard.Review
	encoded, _ := json.Marshal(review)
	var fields map[string]any
	json.Unmarshal(encoded, &fields)
	if len(review.WeakConcepts) != 0 || fields["unmapped_labeled_answered"] != float64(5) {
		t.Fatalf("latest label: %+v", review.WeakConcepts)
	}
	if review.LabeledAnswered != 5 || *review.LabelCoverage != 50 || review.Wrong != 1 || review.Favorites != 1 {
		t.Fatal(review)
	}
	if len(review.Unfinished) != 3 || review.Unfinished[0].StartedAt != "2026-10-04T00:00:00Z" || review.Unfinished[0].SavedAnswers != 1 || review.Unfinished[0].DraftRevision != 3 {
		t.Fatal(review.Unfinished)
	}
	if err = s.Study.RefreshConcepts(context.Background()); err != nil {
		t.Fatal(err)
	}
	dashboard, err = s.Study.Dashboard(context.Background(), 1, 7)
	if err != nil || len(dashboard.Review.WeakConcepts) != 1 || dashboard.Review.WeakConcepts[0].ConceptID <= 0 || dashboard.Review.WeakConcepts[0].Name != "最新考点" || dashboard.Review.WeakConcepts[0].Answered != 5 || dashboard.Review.WeakConcepts[0].Correct != 5 || *dashboard.Review.WeakConcepts[0].Accuracy != 100 {
		t.Fatal(err, dashboard.Review)
	}
}

// Catches bypassing the existing draft revision and submission idempotency
// contracts when a prioritized review creates a session.
func TestPersonalReviewSessionCompatibility(t *testing.T) {
	s, db := fixture(t)
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC) }
	account(t, s.Routes(), "review_compatibility")
	if _, err := db.Exec(`INSERT INTO wrongbook VALUES(1,1,'auto',3,'2026-10-04T00:00:00Z',0)`); err != nil {
		t.Fatal(err)
	}
	session, err := s.Study.PrioritizedWrongbook(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	answers := []study.Answer{{QuestionID: 1, Answer: "H", Duration: 123}}
	revision, err := s.Study.Draft(context.Background(), 1, session.ID, answers, 0)
	if err != nil || revision != 1 {
		t.Fatal(err, revision)
	}
	if _, err = s.Study.Draft(context.Background(), 1, session.ID, answers, 0); !errors.Is(err, study.ErrDraftConflict) {
		t.Fatal(err)
	}
	if _, err = s.Study.Submit(context.Background(), 1, session.ID, answers, revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Study.Submit(context.Background(), 1, session.ID, answers, revision); err != nil {
		t.Fatal(err)
	}
	// Existing submission deliberately uses its established real clock. Assign
	// a fixed historical timestamp only after exercising both real submissions.
	if _, err = db.Exec(`UPDATE practice_session SET submitted_at='2026-10-04T00:00:00Z' WHERE id=?`, session.ID); err != nil {
		t.Fatal(err)
	}
	out, err := s.Study.Dashboard(context.Background(), 1, 7)
	if err != nil || out.Lifetime.Answered != 1 || out.Lifetime.Sessions != 1 || out.Lifetime.DurationMS != 123 {
		t.Fatal(err, out.Lifetime)
	}
}

func TestPersonalDashboardEmptyAndAllHistoryStreak(t *testing.T) {
	s, db := fixture(t)
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC) }
	account(t, s.Routes(), "empty_history")
	out, err := s.Study.Dashboard(context.Background(), 1, 30)
	if err != nil {
		t.Fatal(err)
	}
	if out.Period.Accuracy != nil || out.Lifetime.Answered != 0 || out.Streak.Current != 0 || len(out.Trend) != 30 || len(out.Review.WeakModules) != 0 || out.Review.LabelCoverage != nil {
		t.Fatal(out)
	}
	for _, a := range out.Achievements {
		if a.Unlocked {
			t.Fatal(a)
		}
	}
	for i := 0; i < 30; i++ {
		stamp := time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
		historySession(t, db, 1, stamp, 100, []string{"H"}, []int{1})
	}
	historySession(t, db, 1, "2026-10-03T00:00:00Z", 100, []string{"H"}, []int{1})
	historySession(t, db, 1, "2026-10-03T16:00:00Z", 500, []string{""}, []int{1})
	out, err = s.Study.Dashboard(context.Background(), 1, 7)
	if err != nil {
		t.Fatal(err)
	}
	if out.Streak.Current != 1 || out.Streak.Longest != 30 || out.Streak.PracticedToday || out.Today.Answered != 0 || out.Today.Accuracy != nil || out.Today.Sessions != 1 || out.Today.DurationMS != 500 {
		t.Fatal(out.Streak, out.Today)
	}
	if out.Lifetime.Answered != 31 || out.Lifetime.DurationMS != 3600 || out.Achievements[3].Unlocked != true || out.Achievements[4].Unlocked != true {
		t.Fatal(out.Lifetime, out.Achievements)
	}
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 5, 5, 0, 0, 0, time.UTC) }
	out, err = s.Study.Dashboard(context.Background(), 1, 90)
	if err != nil || out.Streak.Current != 0 || out.Streak.Longest != 30 {
		t.Fatal(err, out.Streak)
	}
}

func TestPersonalDashboardAchievementThresholds(t *testing.T) {
	s, db := fixture(t)
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC) }
	account(t, s.Routes(), "achievement_user")
	names := []string{"政治理论", "常识判断", "言语理解与表达", "数量关系", "判断推理", "资料分析"}
	for i, name := range names {
		if _, err := db.Exec(`INSERT INTO question(id,content_hash,module,stem,answer,answer_type) VALUES(?, ?,?,'成就合成题','A','single')`, 10+i, fmt.Sprint(10+i), name); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 1000; i++ {
		result, err := db.Exec(`INSERT INTO practice_session(user_id,kind,spec,question_ids,started_at,submitted_at,total,duration_ms) VALUES(1,'single','{}',?,'2026-10-04T00:00:00Z','2026-10-04T00:00:00Z',1,1)`, fmt.Sprintf("[%d]", 10+i%6))
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		if _, err = db.Exec(`INSERT INTO practice_answer(session_id,question_id,ord,user_answer,is_correct,duration_ms,answered_at) VALUES(?,?,1,'A',1,1,'2026-10-04T00:00:00Z')`, id, 10+i%6); err != nil {
			t.Fatal(err)
		}
		if i == 98 || i == 99 || i == 998 || i == 999 {
			out, err := s.Study.Dashboard(context.Background(), 1, 7)
			if err != nil {
				t.Fatal(err)
			}
			if out.Achievements[1].Unlocked != (i >= 99) || out.Achievements[2].Unlocked != (i >= 999) || out.Achievements[5].Current != 6 || !out.Achievements[5].Unlocked {
				t.Fatal(i, out.Achievements)
			}
		}
	}
}

func seedExportHistory(t *testing.T, db *sql.DB, rows int) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = tx.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO practice_session(id,user_id,kind,spec,question_ids,started_at,submitted_at,total,duration_ms) SELECT x,1,'single','{}','[1]','2026-10-04T00:00:00Z','2026-10-04T00:00:00Z',1,100 FROM n`, rows)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(`INSERT INTO practice_answer(session_id,question_id,ord,user_answer,is_correct,duration_ms,answered_at) SELECT id,1,1,'H',1,100,submitted_at FROM practice_session`)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestPersonalExportLimitAndDates(t *testing.T) {
	s, db := fixture(t)
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC) }
	h := s.Routes()
	cookie := account(t, h, "export_limit")
	seedExportHistory(t, db, 5000)
	ts := httptest.NewServer(h)
	defer ts.Close()
	status, body, _ := dashboardHTTP(t, ts, "GET", "/api/account/export?days=all", cookie)
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(body), "\ufeff"))).ReadAll()
	if status != 200 || err != nil || len(rows) != 5001 {
		t.Fatal(status, err, len(rows))
	}
	historySession(t, db, 1, "2026-10-04T00:00:00Z", 100, []string{"H"}, []int{1})
	status, body, headers := dashboardHTTP(t, ts, "GET", "/api/account/export?days=all", cookie)
	if status != 400 || strings.HasPrefix(string(body), "\ufeff") || headers.Get("Content-Disposition") != "" {
		t.Fatal(status, headers, string(body))
	}
	if _, err = db.Exec(`DELETE FROM practice_answer`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM practice_session`); err != nil {
		t.Fatal(err)
	}
	for _, stamp := range []string{"2026-09-28T00:00:00+08:00", "2026-09-27T23:59:59+08:00", "2026-09-05T00:00:00+08:00", "2026-09-04T23:59:59+08:00", "2026-07-07T00:00:00+08:00", "2026-07-06T23:59:59+08:00"} {
		historySession(t, db, 1, stamp, 100, []string{"H"}, []int{1})
	}
	for _, want := range []struct {
		days  string
		count int
	}{{"7", 1}, {"30", 3}, {"90", 5}, {"all", 6}} {
		body, err := s.Study.ExportCSV(context.Background(), 1, want.days)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(body), "\ufeff"))).ReadAll()
		if err != nil || len(rows) != want.count+1 {
			t.Fatal(want, err, len(rows))
		}
	}
}

func TestPersonalDashboardConcurrentHistoryAndCancellation(t *testing.T) {
	s, db := fixture(t)
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC) }
	account(t, s.Routes(), "concurrent_analysis")
	account(t, s.Routes(), "concurrent_other")
	// Holding a write transaction must not block a dashboard read snapshot.
	writer, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec(`UPDATE user_profile SET daily_questions=40 WHERE user_id=1`); err != nil {
		writer.Rollback()
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	out, err := s.Study.Dashboard(ctx, 1, 30)
	cancel()
	writer.Rollback()
	if err != nil || out.Profile.DailyQuestions != 20 {
		t.Fatal("analysis held write lock or read dirty profile", err, out.Profile)
	}
	// Exercise the lock contract on a small snapshot, then load larger history.
	// Race-instrumented SQLite query execution is unrelated to lock acquisition.
	seedExportHistory(t, db, 5000)
	historySession(t, db, 2, "2026-10-04T00:00:00Z", 9000, []string{"H"}, []int{1})
	began := time.Now()
	errs := make(chan error, 32)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			// Leave race-instrumented SQLite CPU execution room; the independent
			// small-snapshot lock assertion above retains its strict 2-second limit.
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			user := int64(1 + index%2)
			dashboard, err := s.Study.Dashboard(ctx, user, 90)
			if err != nil {
				errs <- err
				return
			}
			want := 5000
			if user == 2 {
				want = 1
			}
			if dashboard.Period.Answered != want {
				errs <- fmt.Errorf("user %d answered %d", user, dashboard.Period.Answered)
			}
			if _, err = s.Study.ExportCSV(ctx, user, "all"); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	t.Logf("16 concurrent dashboard+export requests, 5000 Alice and 1 Bob records: %s", time.Since(began))
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err = s.Study.Dashboard(ctx, 1, 30); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = s.Study.ExportCSV(ctx, 1, "all"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if db.Stats().InUse != 0 {
		t.Fatal("connection/rows leaked", db.Stats())
	}
}

func TestPersonalExportAndReviewHTTP(t *testing.T) {
	s, db := fixture(t)
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC) }
	h := s.Routes()
	alice := account(t, h, "export_alice")
	account(t, h, "export_bob")
	historySession(t, db, 1, "2026-10-03T16:00:00Z", 100, []string{"=SUM(1,2)\n\"中文\"", ""}, []int{0, 0})
	historySession(t, db, 2, "2026-10-03T16:00:00Z", 100, []string{"BOB_PRIVATE"}, []int{0})
	historySession(t, db, 1, nil, 100, []string{"DRAFT_PRIVATE"}, []int{0})
	ts := httptest.NewServer(h)
	defer ts.Close()
	status, body, headers := dashboardHTTP(t, ts, "GET", "/api/account/export?days=7", alice)
	if status != 200 {
		t.Fatalf("export route want 200 got %d: %s", status, body)
	}
	if !strings.HasPrefix(string(body), "\ufeff") || !strings.HasPrefix(headers.Get("Content-Type"), "text/csv") {
		t.Fatal(headers, string(body))
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(body), "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[1][5] != "'=SUM(1,2)\n\"中文\"" || strings.Contains(string(body), "PRIVATE") || strings.Contains(string(body), "官方解释") {
		t.Fatal(rows, string(body))
	}
	status, body, _ = dashboardHTTP(t, ts, "POST", "/api/account/review/wrongbook", alice)
	if status != 400 {
		t.Fatalf("empty wrong review want 400 got %d: %s", status, body)
	}
	for _, q := range []string{"INSERT INTO wrongbook VALUES(1,1,'auto',3,'2026-10-01T00:00:00Z',0)", "INSERT INTO wrongbook VALUES(1,2,'auto',5,'2026-09-30T00:00:00Z',0)", "INSERT INTO wrongbook VALUES(1,3,'auto',5,'2026-10-03T00:00:00Z',0)", "INSERT INTO wrongbook VALUES(1,4,'auto',99,'2026-10-03T00:00:00Z',0)", "INSERT INTO wrongbook VALUES(1,5,'auto',99,'2026-10-03T00:00:00Z',1)", "INSERT INTO wrongbook VALUES(2,5,'auto',999,'2026-10-03T00:00:00Z',0)"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	status, body, _ = dashboardHTTP(t, ts, "POST", "/api/account/review/wrongbook", alice)
	var session map[string]any
	json.Unmarshal(body, &session)
	if status != 200 || session["kind"] != "wrongbook" || fmt.Sprint(session["question_ids"]) != "[3 2 1]" {
		t.Fatal(status, string(body))
	}
	for _, days := range []string{"8", "-1", "bad"} {
		status, _, _ = dashboardHTTP(t, ts, "GET", "/api/account/export?days="+days, alice)
		if status != 400 {
			t.Fatal(days, status)
		}
	}
}

func TestPersonalDashboardWindowBoundaries(t *testing.T) {
	s, db := fixture(t)
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC) }
	account(t, s.Routes(), "window_boundaries")
	for _, date := range []string{"2026-10-04", "2026-09-28", "2026-09-27", "2026-09-21", "2026-09-20", "2026-09-05", "2026-09-04", "2026-08-06", "2026-08-05", "2026-07-07", "2026-07-06", "2026-04-08", "2026-04-07", "2026-10-05"} {
		historySession(t, db, 1, date+"T00:00:00+08:00", 100, []string{"H"}, []int{1})
	}
	for _, want := range []struct {
		days, period, previous int
		start                  string
	}{{7, 2, 2, "2026-09-28"}, {30, 6, 2, "2026-09-05"}, {90, 10, 2, "2026-07-07"}} {
		out, err := s.Study.Dashboard(context.Background(), 1, want.days)
		if err != nil || out.StartDate != want.start || out.Period.Answered != want.period || out.Previous.Answered != want.previous || out.Lifetime.Answered != 13 || out.Lifetime.DurationMS != 1300 {
			t.Fatal(want, err, out.StartDate, out.Period, out.Previous, out.Lifetime)
		}
	}
}

func TestPersonalReviewMaxTwentyAndSecurity(t *testing.T) {
	s, db := fixture(t)
	h := s.Routes()
	cookie := account(t, h, "review_cap")
	other := account(t, h, "review_cap_other")
	for i := 10; i < 35; i++ {
		if _, err := db.Exec(`INSERT INTO question(id,content_hash,module,stem,answer,answer_type) VALUES(?,?,'资料分析','错题合成题','A','single')`, i, fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO option(question_id,ord,label,content,is_correct) VALUES(?,1,'A','合成选项',1)`, i); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO wrongbook VALUES(1,?,'auto',?,'2026-10-04T00:00:00Z',0)`, i, i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO wrongbook VALUES(1,1,'manual',10000,'2026-10-04T00:00:00Z',0),(1,2,'auto',10000,'2026-10-04T00:00:00Z',1),(2,3,'auto',10000,'2026-10-04T00:00:00Z',0)`); err != nil {
		t.Fatal(err)
	}
	session, err := s.Study.PrioritizedWrongbook(context.Background(), 1)
	if err != nil || len(session.QuestionIDs) != 20 || session.QuestionIDs[0] != 34 || session.QuestionIDs[19] != 15 {
		t.Fatal(err, session.QuestionIDs)
	}
	if _, err = s.Study.Session(context.Background(), 2, session.ID); !errors.Is(err, study.ErrNotFound) {
		t.Fatal(err)
	}
	for _, check := range []struct {
		method, path, origin, ct string
		cookie                   *http.Cookie
		status                   int
	}{
		{"POST", "/api/account/review/wrongbook", "https://evil.example", "application/json", cookie, 403},
		{"POST", "/api/account/review/wrongbook", "http://example.com", "text/plain", cookie, 415},
		{"POST", "/api/account/review/wrongbook", "http://example.com", "application/json", nil, 401},
		{"GET", "/api/account/dashboard?user_id=1", "", "", &http.Cookie{Name: sessionCookie, Value: cookie.Value}, 401},
		{"GET", "/api/account/export?user_id=1", "", "", nil, 401},
	} {
		r := httptest.NewRequest(check.method, check.path, strings.NewReader(`{}`))
		r.Header.Set("Origin", check.origin)
		r.Header.Set("Content-Type", check.ct)
		if check.cookie != nil {
			r.AddCookie(check.cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != check.status {
			t.Fatal(check.path, w.Code, w.Body.String())
		}
	}
	w, v := requestTest(t, h, "GET", "/api/account/dashboard?user_id=1", "", other)
	if w.Code != 200 || v["review"].(map[string]any)["wrong"] != float64(1) || len(v["review"].(map[string]any)["unfinished"].([]any)) != 0 {
		t.Fatal(w.Code, v)
	}
}

func TestPersonalWeakSuggestionsSampleThresholdAndLimit(t *testing.T) {
	s, db := fixture(t)
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC) }
	account(t, s.Routes(), "weak_samples")
	names := []string{"政治理论", "常识判断", "言语理解与表达", "数量关系", "判断推理", "资料分析", "未来模块"}
	for i, name := range names {
		if _, err := db.Exec(`INSERT INTO question(id,content_hash,module,stem,answer,answer_type) VALUES(?, ?,?,'样本合成题','A','single')`, 10+i, fmt.Sprint(10+i), name); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO option(question_id,ord,label,content,is_correct) VALUES(?,1,'A','合成选项',1)`, 10+i); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO label(question_id,run_id,subject,secondary,tertiary) VALUES(?,'new',?,'类',?)`, 10+i, name, "考点"+name); err != nil {
			t.Fatal(err)
		}
		n := 5
		if i == 0 {
			n = 4
		}
		correct := i - 1
		if i == 6 {
			correct = 5
		}
		for j := 0; j < n; j++ {
			result, err := db.Exec(`INSERT INTO practice_session(user_id,kind,spec,question_ids,started_at,submitted_at,total,duration_ms) VALUES(1,'single','{}',?,'2026-10-04T00:00:00Z','2026-10-04T00:00:00Z',1,10)`, fmt.Sprintf("[%d]", 10+i))
			if err != nil {
				t.Fatal(err)
			}
			id, _ := result.LastInsertId()
			ok := 0
			if j < correct {
				ok = 1
			}
			if _, err = db.Exec(`INSERT INTO practice_answer(session_id,question_id,ord,user_answer,is_correct,duration_ms,answered_at) VALUES(?,?,1,'A',?,10,'2026-10-04T00:00:00Z')`, id, 10+i, ok); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.Study.RefreshConcepts(context.Background()); err != nil {
		t.Fatal(err)
	}
	out, err := s.Study.Dashboard(context.Background(), 1, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Modules) != 7 || out.Modules[6].Module != "未来模块" || out.Modules[0].Sufficient || out.Modules[0].Answered != 4 || len(out.Review.WeakModules) != 3 || len(out.Review.WeakConcepts) != 3 {
		t.Fatal(out.Modules, out.Review)
	}
	if out.Review.WeakModules[0].Module != "常识判断" || *out.Review.WeakModules[0].Accuracy != 0 || out.Review.WeakModules[1].Module != "言语理解与表达" || *out.Review.WeakModules[1].Accuracy != 20 || out.Review.WeakModules[2].Module != "数量关系" || *out.Review.WeakModules[2].Accuracy != 40 {
		t.Fatal(out.Review.WeakModules)
	}
	for _, c := range out.Review.WeakConcepts {
		if c.Answered != 5 || c.ConceptID <= 0 {
			t.Fatal(c)
		}
		if _, err := s.Study.CreateSession(context.Background(), 1, "concept", study.Spec{Filter: study.Filter{ConceptID: c.ConceptID}, Limit: 1}); err != nil {
			t.Fatal(err, c)
		}
	}
}

// A shared, legally nullable question module must not break an empty user's
// dashboard, or this user's totals, export and playable wrongbook session.
func TestPersonalReviewNullModuleCompatibility(t *testing.T) {
	s, db := fixture(t)
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC) }
	h := s.Routes()
	alice, bob := account(t, h, "nullable_module_alice"), account(t, h, "nullable_module_bob")
	if _, err := db.Exec(`UPDATE question SET module=NULL WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	w, v := requestTest(t, h, "GET", "/api/account/dashboard?days=7", "", bob)
	if w.Code != 200 {
		t.Errorf("empty user dashboard got %d: %s", w.Code, w.Body.String())
	} else if v["lifetime"].(map[string]any)["answered"] != float64(0) {
		t.Error(v)
	}
	for i := 0; i < 5; i++ {
		historySession(t, db, 1, "2026-10-04T00:00:00Z", 100, []string{"H"}, []int{1})
	}
	if _, err := db.Exec(`INSERT INTO wrongbook VALUES(1,1,'auto',5,'2026-10-04T00:00:00Z',0)`); err != nil {
		t.Fatal(err)
	}
	out, err := s.Study.Dashboard(context.Background(), 1, 7)
	if err != nil {
		t.Errorf("submitted nullable module dashboard: %v", err)
	} else {
		if out.Lifetime.Answered != 5 || out.Period.Answered != 5 || out.Today.Answered != 5 || out.Today.DurationMS != 500 || out.Today.Sessions != 5 || *out.Today.Accuracy != 100 {
			t.Error(out.Lifetime, out.Period, out.Today)
		}
		if len(out.Modules) != 6 || len(out.Review.WeakModules) != 0 || out.Achievements[5].Current != 0 {
			t.Error(out.Modules, out.Review.WeakModules, out.Achievements[5])
		}
		for _, m := range out.Modules {
			if m.Answered != 0 {
				t.Errorf("unknown module assigned to core: %+v", m)
			}
		}
		if len(out.Review.WrongCandidates) != 1 || out.Review.WrongCandidates[0].Module != "" {
			t.Error(out.Review.WrongCandidates)
		}
	}
	w, _ = requestTest(t, h, "GET", "/api/account/export?days=7", "", alice)
	if w.Code != 200 {
		t.Errorf("nullable module CSV got %d: %s", w.Code, w.Body.String())
	} else {
		rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
		if err != nil || len(rows) != 6 || rows[1][4] != "" || rows[1][5] != "H" {
			t.Error(err, rows)
		}
	}
	w, _ = requestTest(t, h, "POST", "/api/account/review/wrongbook", "{}", alice)
	if w.Code != 200 {
		t.Errorf("nullable module wrongbook got %d: %s", w.Code, w.Body.String())
	} else {
		var session study.Session
		if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		if len(session.QuestionIDs) != 1 || session.QuestionIDs[0] != 1 {
			t.Error(session.QuestionIDs)
		}
		if _, err := s.Study.Submit(context.Background(), 1, session.ID, []study.Answer{{QuestionID: 1, Answer: "H", Duration: 10}}, session.DraftRevision); err != nil {
			t.Errorf("nullable module review not playable: %v", err)
		}
	}
}

func TestPersonalReviewUnmappedEmptyTertiary(t *testing.T) {
	for _, test := range []struct {
		name     string
		tertiary any
	}{{"null", nil}, {"empty", ""}} {
		t.Run(test.name, func(t *testing.T) {
			s, db := fixture(t)
			s.Study.Clock = func() time.Time { return time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC) }
			account(t, s.Routes(), "unmapped_empty")
			if _, err := db.Exec(`UPDATE label SET tertiary=? WHERE question_id=1`, test.tertiary); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5; i++ {
				historySession(t, db, 1, "2026-10-04T00:00:00Z", 100, []string{"H"}, []int{1})
			}
			out, err := s.Study.Dashboard(context.Background(), 1, 7)
			if err != nil {
				t.Fatal(err)
			}
			if out.Review.LabeledAnswered != 5 || out.Review.UnmappedLabeledAnswered != 5 || out.Review.LabelCoverage == nil || *out.Review.LabelCoverage != 100 || len(out.Review.WeakConcepts) != 0 {
				t.Fatalf("latest empty tertiary: %+v", out.Review)
			}
		})
	}
}
