package serve

import (
	"fmt"
	"sync"
	"testing"
)

func TestDraftRejectsStaleSnapshotAndSubmission(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	cookie := account(t, h, "draft_user")
	w, v := requestTest(t, h, "POST", "/api/practice/sessions", `{"kind":"single","spec":{"question_ids":[1,2]}}`, cookie)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	path := fmt.Sprintf("/api/practice/sessions/%d", int(v["session_id"].(float64)))
	first := `{"draft_revision":0,"answers":[{"question_id":1,"answer":"H"}]}`
	stale := `{"draft_revision":0,"answers":[{"question_id":2,"answer":"AC"}]}`
	w, _ = requestTest(t, h, "PUT", path+"/answers", first, cookie)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w, _ = requestTest(t, h, "PUT", path+"/answers", stale, cookie)
	if w.Code != 409 {
		t.Fatalf("stale draft overwrote newer answers: HTTP %d %s", w.Code, w.Body.String())
	}
	w, _ = requestTest(t, h, "POST", path+"/submit", stale, cookie)
	if w.Code != 409 {
		t.Fatalf("stale submit accepted: %d %s", w.Code, w.Body.String())
	}
	w, v = requestTest(t, h, "GET", path, "", cookie)
	if w.Code != 200 || v["draft_revision"] != float64(1) || v["submitted_at"] != "" {
		t.Fatal(v)
	}
	answers := v["answers"].([]any)
	if len(answers) != 1 || answers[0].(map[string]any)["answer"] != "H" {
		t.Fatal("newer answers lost", v)
	}
	for _, body := range []string{`{"answers":[]}`, `{"draft_revision":-1,"answers":[]}`} {
		for _, method := range []string{"PUT", "POST"} {
			endpoint := "/answers"
			if method == "POST" {
				endpoint = "/submit"
			}
			w, _ = requestTest(t, h, method, path+endpoint, body, cookie)
			if w.Code != 400 {
				t.Fatal("missing/negative revision accepted", method, w.Code)
			}
		}
	}
	w, v = requestTest(t, h, "PUT", path+"/answers", `{"draft_revision":1,"answers":[{"question_id":1,"answer":"H"},{"question_id":2,"answer":"AC"}]}`, cookie)
	if w.Code != 200 || v["draft_revision"] != float64(2) {
		t.Fatal("current revision rejected", w.Body.String())
	}
	w, v = requestTest(t, h, "POST", path+"/submit", `{"draft_revision":2,"answers":[{"question_id":1,"answer":"H"},{"question_id":2,"answer":"AC"}]}`, cookie)
	if w.Code != 200 || v["correct"] != float64(2) {
		t.Fatal(w.Body.String())
	}
	// Retries after a successful submission must return the existing result,
	// without applying the old snapshot or counting another attempt.
	w, v = requestTest(t, h, "POST", path+"/submit", stale, cookie)
	if w.Code != 200 || v["correct"] != float64(2) {
		t.Fatal("idempotent result lost", w.Body.String())
	}
}

func TestConcurrentDraftCAS(t *testing.T) {
	s, _ := fixture(t)
	h := s.Routes()
	cookie := account(t, h, "cas_user")
	_, v := requestTest(t, h, "POST", "/api/practice/sessions", `{"kind":"single","spec":{"question_ids":[1]}}`, cookie)
	path := fmt.Sprintf("/api/practice/sessions/%d", int(v["session_id"].(float64)))
	start := make(chan struct{})
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, answer := range []string{"A", "H"} {
		wg.Add(1)
		go func(answer string) {
			defer wg.Done()
			<-start
			w, _ := requestTest(t, h, "PUT", path+"/answers", fmt.Sprintf(`{"draft_revision":0,"answers":[{"question_id":1,"answer":%q}]}`, answer), cookie)
			results <- w.Code
		}(answer)
	}
	close(start)
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for code := range results {
		counts[code]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal("concurrent stale writes accepted", counts)
	}
}
