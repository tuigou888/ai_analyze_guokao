package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"ai_analyze_guokao/internal/setting"
	"ai_analyze_guokao/internal/study"
)

// 免费额度配置键必须真正接进 serve：默认 3 卷；改配置立即按新值判定；
// 超限走 429 并带 Retry-After（秒，指向次日零点重置）。
func TestQuotaFromSettings(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	ctx := context.Background()
	c := account(t, s, "quota-free")
	createPaper := func(user *http.Cookie) *httptest.ResponseRecorder {
		w, _ := requestTest(t, h, "POST", "/api/practice/sessions", `{"kind":"paperset","spec":{"paper_id":1}}`, user)
		return w
	}
	for i := 0; i < 3; i++ {
		if w := createPaper(c); w.Code != 200 {
			t.Fatalf("paper %d: HTTP %d %s", i+1, w.Code, w.Body.String())
		}
	}
	w := createPaper(c)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("4th paper: HTTP %d %s, want 429", w.Code, w.Body.String())
	}
	retry := w.Header().Get("Retry-After")
	seconds, err := strconv.Atoi(retry)
	if err != nil || seconds <= 0 || seconds > 86400 {
		t.Fatalf("Retry-After = %q, want seconds in (0,86400]", retry)
	}

	if err := s.Settings.Set(ctx, setting.KeyDailyFreePapers, "5", "test"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if w := createPaper(c); w.Code != 200 {
			t.Fatalf("paper %d after limit=5: HTTP %d %s", i+4, w.Code, w.Body.String())
		}
	}
	if w := createPaper(c); w.Code != http.StatusTooManyRequests {
		t.Fatalf("6th paper after limit=5: HTTP %d %s, want 429", w.Code, w.Body.String())
	}

	if err := s.Settings.Set(ctx, setting.KeyDailyFreePapers, "3", "test"); err != nil {
		t.Fatal(err)
	}
	if w := createPaper(c); w.Code != http.StatusTooManyRequests {
		t.Fatalf("paper after limit back to 3: HTTP %d %s, want 429", w.Code, w.Body.String())
	}

	// "0" 视为未配置，回落默认 3：新用户前 3 卷可用，第 4 卷拒绝。
	if err := s.Settings.Set(ctx, setting.KeyDailyFreePapers, "0", "test"); err != nil {
		t.Fatal(err)
	}
	fresh := account(t, s, "quota-fallback")
	for i := 0; i < 3; i++ {
		if w := createPaper(fresh); w.Code != 200 {
			t.Fatalf("fallback paper %d: HTTP %d %s", i+1, w.Code, w.Body.String())
		}
	}
	if w := createPaper(fresh); w.Code != http.StatusTooManyRequests {
		t.Fatalf("fallback 4th paper: HTTP %d %s, want 429", w.Code, w.Body.String())
	}
}

// 午夜前不足 1 秒时也要向上取整为 1：Retry-After 不许出现 0。
func TestSecondsUntilTomorrow(t *testing.T) {
	s, _ := fixture(t)
	for _, c := range []struct {
		now  time.Time
		want int64
	}{
		{time.Date(2026, 10, 18, 23, 59, 59, 500_000_000, study.ExamZone), 1},
		{time.Date(2026, 10, 18, 12, 0, 0, 0, study.ExamZone), 43200},
	} {
		now := c.now
		s.Study.Clock = func() time.Time { return now }
		if got := s.secondsUntilTomorrow(); got != c.want {
			t.Errorf("secondsUntilTomorrow at %s = %d, want %d", c.now, got, c.want)
		}
	}
}
