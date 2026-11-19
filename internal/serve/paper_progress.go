package serve

import (
	"net/http"
	"strconv"
	"strings"

	"ai_analyze_guokao/internal/study"
)

func (s *Server) paperProgress(w http.ResponseWriter, r *http.Request) {
	raw := strings.Split(r.URL.Query().Get("paper_ids"), ",")
	if len(raw) > 100 {
		s.studyError(w, study.ErrInvalid)
		return
	}
	ids := make([]int64, 0, len(raw))
	for _, value := range raw {
		id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || id <= 0 {
			s.studyError(w, study.ErrInvalid)
			return
		}
		ids = append(ids, id)
	}
	items, err := s.Study.PaperProgress(r.Context(), currentUser(r).ID, ids)
	s.respond(w, map[string]any{"items": items}, err)
}
