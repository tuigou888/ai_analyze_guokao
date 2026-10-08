package serve

import (
	"fmt"
	"testing"
)

func TestWrongCurrentPasswordPreservesAuthentication(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	cookie := account(t, h, "password_user")
	w, _ := requestTest(t, h, "POST", "/api/auth/password", `{"current_password":"wrong-password","password":"new-test-password"}`, cookie)
	if w.Code != 400 {
		t.Fatalf("business validation emitted session-expired status: %d %s", w.Code, w.Body.String())
	}
	w, _ = requestTest(t, h, "GET", "/api/auth/me", "", cookie)
	if w.Code != 200 {
		t.Fatal("wrong current password revoked valid session", w.Code)
	}
	w, _ = requestTest(t, h, "POST", "/api/auth/login", `{"username":"password_user","password":"testpassword"}`, nil)
	if w.Code != 200 {
		t.Fatal("password changed after failed validation", w.Code)
	}
}

func TestRevealUsesNonemptySubmittedAnswer(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	alice := account(t, h, "reveal_alice")
	bob := account(t, h, "reveal_bob")
	_, v := requestTest(t, h, "POST", "/api/practice/sessions", `{"kind":"single","spec":{"question_ids":[1,2]}}`, alice)
	path := fmt.Sprintf("/api/practice/sessions/%d", int(v["session_id"].(float64)))
	w, _ := requestTest(t, h, "PUT", path+"/answers", `{"draft_revision":0,"answers":[{"question_id":1,"answer":"A"}]}`, alice)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w, _ = requestTest(t, h, "GET", "/api/questions/1/reveal", "", alice)
	if w.Code != 403 {
		t.Fatal("draft unlocked reveal", w.Code)
	}
	w, _ = requestTest(t, h, "POST", path+"/submit", `{"draft_revision":1,"answers":[{"question_id":1,"answer":"A"}]}`, alice)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, check := range []struct {
		id    int
		other bool
		code  int
	}{{1, false, 200}, {2, false, 403}, {1, true, 403}} {
		cookie := alice
		if check.other {
			cookie = bob
		}
		w, v = requestTest(t, h, "GET", fmt.Sprintf("/api/questions/%d/reveal", check.id), "", cookie)
		if w.Code != check.code {
			t.Fatalf("reveal %d other=%t: %d %s", check.id, check.other, w.Code, w.Body.String())
		}
		if check.code == 200 && v["answer"] != "H" {
			t.Fatal("no explanation unlocked", v)
		}
	}
}
