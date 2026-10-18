package study

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"ai_analyze_guokao/internal/store"
)

// Catches missing transaction quota enforcement, paperset/question accounting
// mix-ups, VIP bypass regressions and stale daily_usage counters.
func TestDailyQuotaHardLimit(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "quota.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stamp := time.Date(2026, 10, 16, 10, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	now := stamp
	s := &Service{DB: db, Clock: func() time.Time { return now }}
	tight := &Service{DB: db, Clock: func() time.Time { return now }, Quota: func(context.Context) (int, int) { return 2, 3 }}
	ctx := context.Background()

	if _, err := db.Exec(`INSERT INTO paper(id,name,source_path) VALUES(1,'卷1','quota-test.sqlite');
		INSERT INTO question(id,content_hash,stem,answer,answer_type,option_count) VALUES
		(1,'quota-1','题1','A','single',2),
		(2,'quota-2','题2','B','single',2),
		(3,'quota-3','题3','C','single',2);
		INSERT INTO question_occurrence(question_id,paper_id,number) VALUES(1,1,1),(2,1,2),(3,1,3)`); err != nil {
		t.Fatal(err)
	}
	seedUser := func(username, role string, expires any) int64 {
		t.Helper()
		res, err := db.Exec(`INSERT INTO app_user(username,password_hash,nickname,created_at,role,status,vip_expires_at) VALUES(?,?,?,?,?,?,?)`,
			username, "x", username, stamp.UTC().Format(time.RFC3339), role, "active", expires)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	usage := func(user int64, day string) (questions, papers int) {
		t.Helper()
		err := db.QueryRow(`SELECT COALESCE(questions,0),COALESCE(papers,0) FROM daily_usage WHERE user_id=? AND day=?`, user, day).Scan(&questions, &papers)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
		return questions, papers
	}
	paperUsageRows := func(user int64) int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM daily_usage WHERE user_id=?`, user).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// 默认额度（Quota 为 nil → 3 卷）：第 4 卷拒绝，计数只累加 papers。
	free := seedUser("free_papers", "free", nil)
	for i := 0; i < 3; i++ {
		p, err := s.CreateSession(ctx, free, "paperset", Spec{Filter: Filter{PaperID: 1}})
		if err != nil {
			t.Fatalf("paperset %d: %v", i+1, err)
		}
		if p.Total != 3 {
			t.Fatalf("paperset %d total: %d, want 3", i+1, p.Total)
		}
	}
	if _, err := s.CreateSession(ctx, free, "paperset", Spec{Filter: Filter{PaperID: 1}}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("4th paperset: got %v, want ErrQuotaExceeded", err)
	}
	if q, p := usage(free, "2026-10-16"); q != 0 || p != 3 {
		t.Fatalf("free usage = (%d,%d), want (0,3)", q, p)
	}
	var sessions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM practice_session WHERE user_id=?`, free).Scan(&sessions); err != nil || sessions != 3 {
		t.Fatalf("free sessions = %d (%v), want 3", sessions, err)
	}

	// 题数额度（Quota=(2,3)）：恰好 2 题允许，再 2 题拒绝，questions 只累加题数。
	exact := seedUser("free_questions", "free", nil)
	if _, err := tight.CreateSession(ctx, exact, "single", Spec{QuestionIDs: []int64{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := tight.CreateSession(ctx, exact, "single", Spec{QuestionIDs: []int64{2, 3}}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("second 2-question session: got %v, want ErrQuotaExceeded", err)
	}
	if q, p := usage(exact, "2026-10-16"); q != 2 || p != 0 {
		t.Fatalf("exact usage = (%d,%d), want (2,0)", q, p)
	}

	// 默认题数额度（nil→50）：3 题会话不受影响。
	loose := seedUser("free_loose", "free", nil)
	if _, err := s.CreateSession(ctx, loose, "single", Spec{QuestionIDs: []int64{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	if q, _ := usage(loose, "2026-10-16"); q != 3 {
		t.Fatalf("loose questions = %d, want 3", q)
	}

	// 未到期 VIP：绕过额度且不写 daily_usage。
	vip := seedUser("vip_forever", "vip", nil)
	for i := 0; i < 5; i++ {
		if _, err := s.CreateSession(ctx, vip, "paperset", Spec{Filter: Filter{PaperID: 1}}); err != nil {
			t.Fatalf("vip paperset %d: %v", i+1, err)
		}
	}
	if rows := paperUsageRows(vip); rows != 0 {
		t.Fatalf("vip daily_usage rows = %d, want 0", rows)
	}

	// 到期日当天仍有效：同为 VIP，另有独立计数行断言。
	vipToday := seedUser("vip_today", "vip", "2026-10-16")
	for i := 0; i < 4; i++ {
		if _, err := s.CreateSession(ctx, vipToday, "paperset", Spec{Filter: Filter{PaperID: 1}}); err != nil {
			t.Fatalf("same-day vip paperset %d: %v", i+1, err)
		}
	}
	if rows := paperUsageRows(vipToday); rows != 0 {
		t.Fatalf("same-day vip daily_usage rows = %d, want 0", rows)
	}

	// 已过期 VIP：回落 free 口径，3 卷上限。
	expired := seedUser("vip_expired", "vip", "2026-10-10")
	for i := 0; i < 3; i++ {
		if _, err := s.CreateSession(ctx, expired, "paperset", Spec{Filter: Filter{PaperID: 1}}); err != nil {
			t.Fatalf("expired vip paperset %d: %v", i+1, err)
		}
	}
	if _, err := s.CreateSession(ctx, expired, "paperset", Spec{Filter: Filter{PaperID: 1}}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expired vip 4th paperset: got %v, want ErrQuotaExceeded", err)
	}
	if q, p := usage(expired, "2026-10-16"); q != 0 || p != 3 {
		t.Fatalf("expired vip usage = (%d,%d), want (0,3)", q, p)
	}

	// 次日重置：新一天重新计数，旧日期行保持不变。
	now = now.Add(24 * time.Hour)
	if _, err := s.CreateSession(ctx, free, "paperset", Spec{Filter: Filter{PaperID: 1}}); err != nil {
		t.Fatalf("next-day paperset: %v", err)
	}
	if q, p := usage(free, "2026-10-16"); q != 0 || p != 3 {
		t.Fatalf("next-day old usage = (%d,%d), want (0,3)", q, p)
	}
	if q, p := usage(free, "2026-10-17"); q != 0 || p != 1 {
		t.Fatalf("next-day usage = (%d,%d), want (0,1)", q, p)
	}
}

func TestQuotaDay(t *testing.T) {
	cases := []struct {
		in   time.Time
		want string
	}{
		{time.Date(2026, 10, 16, 18, 0, 0, 0, time.UTC), "2026-10-17"},
		{time.Date(2026, 10, 16, 15, 59, 59, 0, time.UTC), "2026-10-16"},
		{time.Date(2026, 10, 16, 16, 0, 0, 0, time.UTC), "2026-10-17"},
	}
	for _, c := range cases {
		if got := QuotaDay(c.in); got != c.want {
			t.Errorf("QuotaDay(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}
