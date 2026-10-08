package serve

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"ai_analyze_guokao/internal/store"
	"ai_analyze_guokao/internal/study"
)

// Uses independent SQLite pools as well as HTTP authentication and grading.
// A process-local mutex would not fix concurrent writes from another process.
func TestConcurrentSubmissions(t *testing.T) {
	for _, sameSession := range []bool{false, true} {
		t.Run(fmt.Sprintf("same_session_%t", sameSession), func(t *testing.T) {
			s, db := fixture(t)
			h := s.Routes()
			var file string
			if e := db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&file); e != nil {
				t.Fatal(e)
			}
			secondDB, e := store.Open(file)
			if e != nil {
				t.Fatal(e)
			}
			defer secondDB.Close()
			secondHandler := New(s.Settings, secondDB, nil).Routes()
			const n = 20
			cookies := make([]*http.Cookie, n)
			ids := make([]int64, n)
			for i := 0; i < n; i++ {
				if sameSession && i > 0 {
					cookies[i], ids[i] = cookies[0], ids[0]
					continue
				}
				u, e := s.Study.Register(context.Background(), fmt.Sprintf("parallel_%d", i), "testpassword", "")
				if e != nil {
					t.Fatal(e)
				}
				token, _, e := s.Study.Login(context.Background(), u.Username, "testpassword")
				if e != nil {
					t.Fatal(e)
				}
				cookies[i] = &http.Cookie{Name: userCookie, Value: token}
				p, e := s.Study.CreateSession(context.Background(), u.ID, "single", study.Spec{QuestionIDs: []int64{1, 2, 3, 5}})
				if e != nil {
					t.Fatal(e)
				}
				ids[i] = p.ID
			}
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					handler := h
					if i%2 == 1 {
						handler = secondHandler
					}
					w, v := requestTest(t, handler, "POST", fmt.Sprintf("/api/practice/sessions/%d/submit", ids[i]), `{"draft_revision":0,"answers":[{"question_id":1,"answer":"A"},{"question_id":2,"answer":"AC"},{"question_id":3,"answer":"B"},{"question_id":5,"answer":"A"}]}`, cookies[i])
					if w.Code != 200 {
						t.Errorf("submit %d: HTTP %d %s", i, w.Code, w.Body.String())
						return
					}
					if v["correct"] != float64(3) {
						t.Errorf("incorrect grade: %v", v["correct"])
					}
				}(i)
			}
			close(start)
			wg.Wait()
			expected := n
			if sameSession {
				expected = 1
			}
			for _, check := range []struct {
				query string
				want  int
			}{
				{`SELECT COUNT(*) FROM practice_session WHERE submitted_at IS NOT NULL`, expected},
				{`SELECT COUNT(*) FROM practice_answer`, expected * 4},
				{`SELECT SUM(wrong_count) FROM wrongbook`, expected},
				{`SELECT SUM(answered) FROM user_concept_stat`, expected},
			} {
				var got int
				if e := db.QueryRow(check.query).Scan(&got); e != nil {
					t.Fatal(e)
				}
				if got != check.want {
					t.Errorf("%s: got %d want %d", check.query, got, check.want)
				}
			}
		})
	}
}

func TestWriteSaturationReturnsRetryableResponse(t *testing.T) {
	s, _ := fixture(t)
	response := httptest.NewRecorder()
	s.studyError(response, store.ErrWriteBusy)
	if response.Code != 503 || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("writer saturation is not retryable: %d %s", response.Code, response.Body.String())
	}
}
