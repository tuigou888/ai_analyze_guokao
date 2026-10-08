package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

func profileBody(t *testing.T, changes map[string]any) string {
	t.Helper()
	v := map[string]any{"nickname": "新昵称", "bio": "简介", "avatar_id": 7, "daily_questions": 20, "daily_minutes": 30, "exam_name": "国考", "exam_date": "", "default_limit": 20, "default_module": "资料分析", "reading_size": 18, "revision": 0}
	for k, x := range changes {
		v[k] = x
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Catches missing routes/defaults, an implicit GET write, nickname drift and lost CAS.
func TestPersonalProfileDefaultsUpdateCAS(t *testing.T) {
	s, db := fixture(t)
	h := s.Routes()
	alice := account(t, h, "profile_alice")
	bob := account(t, h, "profile_bob")
	var oldHash, oldToken string
	if err := db.QueryRow(`SELECT u.password_hash,s.token FROM app_user u JOIN app_session s ON s.user_id=u.id WHERE u.id=1`).Scan(&oldHash, &oldToken); err != nil {
		t.Fatal(err)
	}
	w, v := requestTest(t, h, "GET", "/api/account/profile", "", alice)
	if w.Code != 200 {
		t.Fatalf("default profile: %d %s", w.Code, w.Body.String())
	}
	for k, want := range map[string]any{"username": "profile_alice", "nickname": "", "bio": "", "avatar_id": float64(0), "daily_questions": float64(20), "daily_minutes": float64(30), "default_limit": float64(20), "reading_size": float64(16), "revision": float64(0)} {
		if v[k] != want {
			t.Fatalf("%s=%v want %v", k, v[k], want)
		}
	}
	if v["created_at"] == "" || v["updated_at"] == "" {
		t.Fatal("missing dates", v)
	}
	if _, err := db.Exec(`DELETE FROM user_profile WHERE user_id=1`); err != nil {
		t.Fatal(err)
	}
	w, _ = requestTest(t, h, "GET", "/api/account/profile", "", alice)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_profile WHERE user_id=1`).Scan(&count); err != nil || count != 0 {
		t.Fatal("GET wrote profile", count, err)
	}
	w, v = requestTest(t, h, "PUT", "/api/account/profile", profileBody(t, nil), alice)
	if w.Code != 200 || v["revision"] != float64(1) || v["nickname"] != "新昵称" {
		t.Fatal("save", w.Code, w.Body.String())
	}
	var newHash, newToken string
	if err := db.QueryRow(`SELECT u.password_hash,s.token FROM app_user u JOIN app_session s ON s.user_id=u.id WHERE u.id=1`).Scan(&newHash, &newToken); err != nil || newHash != oldHash || newToken != oldToken {
		t.Fatal("profile changed credentials", err)
	}
	w, _ = requestTest(t, h, "PUT", "/api/account/profile", profileBody(t, map[string]any{"nickname": "陈旧昵称"}), alice)
	if w.Code != 409 {
		t.Fatal("stale CAS", w.Code, w.Body.String())
	}
	w, v = requestTest(t, h, "GET", "/api/auth/me", "", alice)
	if w.Code != 200 || v["nickname"] != "新昵称" {
		t.Fatal("nickname atomicity", w.Code, v)
	}
	w, v = requestTest(t, h, "GET", "/api/account/profile", "", bob)
	if w.Code != 200 || v["nickname"] != "" || v["revision"] != float64(0) {
		t.Fatal("isolation", w.Code, v)
	}
}

// Catches accepting forbidden enums/targets/dates, missing revisions and client identities.
func TestPersonalProfileRejectsInvalidAndUnauthorized(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	cookie := account(t, h, "profile_validation")
	for _, change := range []map[string]any{{"avatar_id": 8}, {"avatar_id": -1}, {"reading_size": 17}, {"default_limit": 30}, {"daily_questions": 4}, {"daily_questions": 201}, {"daily_minutes": 4}, {"daily_minutes": 181}, {"default_module": "不存在"}, {"bio": strings.Repeat("字", 201)}, {"nickname": strings.Repeat("字", 41)}, {"exam_name": strings.Repeat("字", 41)}, {"exam_date": "2020-01-01"}, {"exam_date": "2099-01-01"}, {"exam_date": "2026-02-30"}, {"revision": -1}, {"revision": nil}, {"user_id": 2}, {"username": "other"}} {
		w, _ := requestTest(t, h, "PUT", "/api/account/profile", profileBody(t, change), cookie)
		if w.Code != 400 {
			t.Fatalf("change %v: %d %s", change, w.Code, w.Body.String())
		}
	}
	w, _ := requestTest(t, h, "GET", "/api/account/profile", "", nil)
	if w.Code != 401 {
		t.Fatal("anonymous", w.Code)
	}
	w, _ = requestTest(t, h, "GET", "/api/account/profile", "", &http.Cookie{Name: sessionCookie, Value: cookie.Value})
	if w.Code != 401 {
		t.Fatal("admin cookie accepted as user", w.Code)
	}
	r := httptest.NewRequest("PUT", "/api/account/profile", strings.NewReader(profileBody(t, nil)))
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	if out.Code != 403 {
		t.Fatal("cross origin", out.Code)
	}
}

// Catches secret exposure, cross-user deletion, current deletion and wrong revoke-others filtering.
func TestPersonalSessionsIsolationAndRevocation(t *testing.T) {
	s, db := fixture(t)
	h := s.Routes()
	alice := account(t, h, "session_alice")
	bob := account(t, h, "session_bob")
	w, _ := requestTest(t, h, "POST", "/api/auth/login", `{"username":"session_alice","password":"testpassword"}`, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	other := w.Result().Cookies()[0]
	w, v := requestTest(t, h, "GET", "/api/account/sessions", "", alice)
	if w.Code != 200 {
		t.Fatalf("sessions: %d %s", w.Code, w.Body.String())
	}
	if v["total"] != float64(2) || v["size"] != float64(20) {
		t.Fatal(v)
	}
	var currentID, otherID string
	for _, value := range v["items"].([]any) {
		row := value.(map[string]any)
		id := row["public_id"].(string)
		if id == "" || id == alice.Value || id == other.Value {
			t.Fatal("credential as ID", row)
		}
		if row["current"] == true {
			currentID = id
		} else {
			otherID = id
		}
		for _, key := range []string{"token", "token_hash", "hash"} {
			if _, ok := row[key]; ok {
				t.Fatal("secret field", row)
			}
		}
	}
	var stored string
	if err := db.QueryRow(`SELECT token FROM app_session WHERE user_id=1 LIMIT 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), stored) {
		t.Fatal("hash leaked")
	}
	for _, check := range []struct {
		id     string
		cookie *http.Cookie
		want   int
	}{{currentID, alice, 400}, {otherID, bob, 404}, {"missing", alice, 404}, {otherID, alice, 200}} {
		w, _ = requestTest(t, h, "DELETE", "/api/account/sessions/"+check.id, "", check.cookie)
		if w.Code != check.want {
			t.Fatal("delete", check.want, w.Code, w.Body.String())
		}
	}
	w, _ = requestTest(t, h, "GET", "/api/auth/me", "", other)
	if w.Code != 401 {
		t.Fatal("revoked cookie remains valid", w.Code)
	}
	w, _ = requestTest(t, h, "POST", "/api/auth/login", `{"username":"session_alice","password":"testpassword"}`, nil)
	third := w.Result().Cookies()[0]
	w, v = requestTest(t, h, "POST", "/api/account/sessions/revoke-others", "", alice)
	if w.Code != 200 || v["revoked"] != float64(1) {
		t.Fatal("revoke others", w.Code, v)
	}
	for _, check := range []struct {
		cookie *http.Cookie
		want   int
	}{{alice, 200}, {third, 401}, {bob, 200}} {
		w, _ = requestTest(t, h, "GET", "/api/auth/me", "", check.cookie)
		if w.Code != check.want {
			t.Fatal("cookie status", w.Code, check.want)
		}
	}
}

// Catches storing raw IPs or trusting a forged XFF and rendering arbitrary UA as device labels.
func TestPersonalLoginMetadata(t *testing.T) {
	s, db := fixture(t)
	h := s.Routes()
	account(t, h, "metadata_user")
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"metadata_user","password":"testpassword"}`))
	r.RemoteAddr = "203.0.113.42:1234"
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	r.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0) Chrome/123.0 Safari/537.36")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	w, v := requestTest(t, h, "GET", "/api/account/sessions", "", cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, x := range v["items"].([]any) {
		row := x.(map[string]any)
		if row["current"] == true {
			if row["ip_hint"] != "203.0.113.*" || row["device_label"] != "Chrome / Windows" || row["created_at"] == nil {
				t.Fatal("metadata", row)
			}
		}
	}
	var raw int
	if err := db.QueryRow(`SELECT COUNT(*) FROM app_session WHERE ip_hint LIKE '%42%' OR ip_hint LIKE '%198.51%'`).Scan(&raw); err != nil || raw != 0 {
		t.Fatal("raw or spoofed IP stored", raw, err)
	}
}

// Catches partial PUTs resetting saved fields and null becoming a valid zero enum.
func TestPersonalProfileRequiresCompleteEditableFields(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	cookie := account(t, h, "complete_profile")
	var input map[string]any
	if err := json.Unmarshal([]byte(profileBody(t, nil)), &input); err != nil {
		t.Fatal(err)
	}
	for key := range input {
		for _, null := range []bool{false, true} {
			var v map[string]any
			_ = json.Unmarshal([]byte(profileBody(t, nil)), &v)
			if null {
				v[key] = nil
			} else {
				delete(v, key)
			}
			raw, _ := json.Marshal(v)
			w, _ := requestTest(t, h, "PUT", "/api/account/profile", string(raw), cookie)
			if w.Code != 400 {
				t.Fatalf("missing/null %s null=%t: %d %s", key, null, w.Code, w.Body.String())
			}
		}
	}
	w, _ := requestTest(t, h, "PUT", "/api/account/profile", profileBody(t, map[string]any{"Avatar_ID": 0}), cookie)
	if w.Code != 400 {
		t.Fatal("non-whitelisted alternate casing", w.Code, w.Body.String())
	}
}

// Catches accepting two concurrent edits from the same revision.
func TestPersonalProfileConcurrentCAS(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	cookie := account(t, h, "concurrent_profile")
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, nickname := range []string{"窗口甲", "窗口乙"} {
		body := profileBody(t, map[string]any{"nickname": nickname})
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("PUT", "/api/account/profile", strings.NewReader(body))
			r.Header.Set("Origin", "http://example.com")
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			codes <- w.Code
		}()
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal(counts)
	}
	w, v := requestTest(t, h, "GET", "/api/account/profile", "", cookie)
	if w.Code != 200 || v["revision"] != float64(1) {
		t.Fatal(w.Code, v)
	}
}

// Catches non-atomic registration and profile/nickname writes when a constraint fails.
func TestPersonalProfileWriteAtomicity(t *testing.T) {
	s, db := fixture(t)
	h := s.Routes()
	cookie := account(t, h, "atomic_profile")
	if _, err := db.Exec(`CREATE TRIGGER fail_nickname BEFORE UPDATE OF nickname ON app_user BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	w, _ := requestTest(t, h, "PUT", "/api/account/profile", profileBody(t, nil), cookie)
	if w.Code != 500 {
		t.Fatal(w.Code, w.Body.String())
	}
	var revision int64
	var bio string
	if err := db.QueryRow(`SELECT revision,bio FROM user_profile WHERE user_id=1`).Scan(&revision, &bio); err != nil || revision != 0 || bio != "" {
		t.Fatal("profile partial commit", revision, bio, err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_profile BEFORE INSERT ON user_profile BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := s.Study.Register(context.Background(), "failed_registration", "testpassword", "")
	if err == nil {
		t.Fatal("failure ignored")
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM app_user WHERE username='failed_registration'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("orphan user", count, err)
	}
}

// Catches wrong Beijing date edges and failures to persist valid enum/Unicode boundaries.
func TestPersonalProfileDateAndUnicodeBoundaries(t *testing.T) {
	s, _ := fixture(t)
	s.Study.Clock = func() time.Time { return time.Date(2026, 10, 4, 16, 30, 0, 0, time.UTC) }
	h := s.Routes()
	cookie := account(t, h, "boundary_profile")
	for _, date := range []string{"2026-10-04", "2026-10-05", "2031-10-06"} {
		w, _ := requestTest(t, h, "PUT", "/api/account/profile", profileBody(t, map[string]any{"exam_date": date}), cookie)
		if w.Code != 400 {
			t.Fatal(date, w.Code, w.Body.String())
		}
	}
	w, v := requestTest(t, h, "PUT", "/api/account/profile", profileBody(t, map[string]any{"exam_date": "2031-10-05", "nickname": strings.Repeat("字", 40), "bio": strings.Repeat("字", 200), "exam_name": strings.Repeat("字", 40), "daily_questions": 200, "daily_minutes": 180, "default_limit": 50, "reading_size": 20}), cookie)
	if w.Code != 200 || v["exam_date"] != "2031-10-05" {
		t.Fatal(w.Code, w.Body.String())
	}
}

// Catches POST actions skipping JSON checks and DELETE actions skipping Origin checks.
func TestPersonalSessionWriteRequestSecurity(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	cookie := account(t, h, "session_security")
	for _, check := range []struct {
		method, path, origin, ct string
		want                     int
	}{
		{"POST", "/api/account/sessions/revoke-others", "http://example.com", "text/plain", 415},
		{"POST", "/api/account/sessions/revoke-others", "https://evil.example", "application/json", 403},
		{"DELETE", "/api/account/sessions/missing", "https://evil.example", "", 403},
		{"DELETE", "/api/account/sessions/missing", "", "", 403},
	} {
		r := httptest.NewRequest(check.method, check.path, nil)
		r.Header.Set("Origin", check.origin)
		r.Header.Set("Content-Type", check.ct)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != check.want {
			t.Fatal(check, w.Code, w.Body.String())
		}
	}
}

// Catches returning expired/foreign sessions, unbounded pages and fabricated historical data.
func TestPersonalSessionsPaginationLegacyAndExpiry(t *testing.T) {
	s, db := fixture(t)
	h := s.Routes()
	cookie := account(t, h, "paged_sessions")
	account(t, h, "other_sessions")
	for i := 0; i < 21; i++ {
		id := fmt.Sprintf("synthetic-public-%02d", i)
		token := fmt.Sprintf("synthetic-hash-%02d", i)
		if _, err := db.Exec(`INSERT INTO app_session(token,user_id,expires_at,public_id) VALUES(?,1,'2099-01-01T00:00:00Z',?)`, token, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO app_session(token,user_id,expires_at,public_id) VALUES('expired-hash',1,'2000-01-01T00:00:00Z','expired-public')`); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, check := range []struct{ page, count int }{{1, 20}, {2, 2}, {3, 0}} {
		w, v := requestTest(t, h, "GET", fmt.Sprintf("/api/account/sessions?page=%d", check.page), "", cookie)
		if w.Code != 200 || v["total"] != float64(22) || v["page"] != float64(check.page) || len(v["items"].([]any)) != check.count {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, x := range v["items"].([]any) {
			row := x.(map[string]any)
			id := row["public_id"].(string)
			if seen[id] || id == "expired-public" {
				t.Fatal("duplicate/expired", id)
			}
			seen[id] = true
			if strings.HasPrefix(id, "synthetic-") && (row["created_at"] != nil || row["device_label"] != nil || row["ip_hint"] != nil) {
				t.Fatal("fabricated metadata", row)
			}
		}
	}
	for _, page := range []string{"-1", "0", "abc", "1000001"} {
		w, _ := requestTest(t, h, "GET", "/api/account/sessions?page="+page, "", cookie)
		if w.Code != 400 {
			t.Fatal(page, w.Code)
		}
	}
}

// Catches dropping the trusted-proxy algorithm or storing an IPv6 host address.
func TestPersonalLoginMetadataTrustedIPv6(t *testing.T) {
	s, _ := fixture(t)
	s.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	h := s.Routes()
	account(t, h, "ipv6_metadata")
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"ipv6_metadata","password":"testpassword"}`))
	r.RemoteAddr = "10.0.0.2:1234"
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Forwarded-For", "2001:db8:abcd:1234::42, 10.0.0.3")
	r.Header.Set("User-Agent", "Mozilla/5.0 (iPhone) CriOS/123.0 Safari/537.36")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	w, v := requestTest(t, h, "GET", "/api/account/sessions", "", cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, x := range v["items"].([]any) {
		row := x.(map[string]any)
		if row["current"] == true && (row["ip_hint"] != "2001:db8:abcd::/48" || row["device_label"] != "Chrome / iOS") {
			t.Fatal(row)
		}
	}
}

// Catches expired saved exam dates blocking unrelated edits or another user's
// old date being accepted as a legacy exception.
func TestPersonalProfilePreservesOwnExpiredExamDate(t *testing.T) {
	s, _ := fixture(t)
	stamp := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	s.Study.Clock = func() time.Time { return stamp }
	h := s.Routes()
	alice := account(t, h, "exam_alice")
	bob := account(t, h, "exam_bob")
	w, _ := requestTest(t, h, "PUT", "/api/account/profile", profileBody(t, map[string]any{"exam_date": "2026-10-05"}), alice)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	stamp = stamp.AddDate(0, 0, 1)
	w, v := requestTest(t, h, "PUT", "/api/account/profile", profileBody(t, map[string]any{"exam_date": "2026-10-05", "revision": 1, "nickname": "日期已到"}), alice)
	if w.Code != 200 || v["exam_date"] != "2026-10-05" {
		t.Fatal("today's original date blocked edit", w.Code, w.Body.String())
	}
	stamp = stamp.AddDate(0, 0, 1)
	w, v = requestTest(t, h, "PUT", "/api/account/profile", profileBody(t, map[string]any{"exam_date": "2026-10-05", "revision": 2, "nickname": "保留过期日期"}), alice)
	if w.Code != 200 || v["revision"] != float64(3) {
		t.Fatal("expired original date blocked edit", w.Code, w.Body.String())
	}
	w, _ = requestTest(t, h, "PUT", "/api/account/profile", profileBody(t, map[string]any{"exam_date": "2026-10-05", "revision": 2}), alice)
	if w.Code != 409 {
		t.Fatal("expired-date exception bypassed CAS", w.Code, w.Body.String())
	}
	w, _ = requestTest(t, h, "PUT", "/api/account/profile", profileBody(t, map[string]any{"exam_date": "2026-10-05"}), bob)
	if w.Code != 400 {
		t.Fatal("other user's expired date accepted", w.Code, w.Body.String())
	}
	w, _ = requestTest(t, h, "PUT", "/api/account/profile", profileBody(t, map[string]any{"exam_date": "2026-10-04", "revision": 3}), alice)
	if w.Code != 400 {
		t.Fatal("changed past date accepted", w.Code, w.Body.String())
	}
	w, v = requestTest(t, h, "PUT", "/api/account/profile", profileBody(t, map[string]any{"exam_date": "", "revision": 3}), alice)
	if w.Code != 200 || v["exam_date"] != "" {
		t.Fatal("cannot clear expired date", w.Code, w.Body.String())
	}
}
