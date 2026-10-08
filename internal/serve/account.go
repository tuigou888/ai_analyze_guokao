package serve

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"ai_analyze_guokao/internal/study"
	"github.com/go-chi/chi/v5"
)

// accountRoutes is registered inside requireUser. Subsequent dashboard/export/
// review handlers share this authenticated account boundary.
func (s *Server) accountRoutes(r chi.Router) {
	r.Get("/account/dashboard", s.accountDashboard)
	r.Get("/account/export", s.accountExport)
	r.Post("/account/review/wrongbook", s.accountWrongbook)
	r.Get("/account/profile", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Study.Profile(r.Context(), currentUser(r).ID)
		s.respond(w, v, e)
	})
	r.Put("/account/profile", s.updateProfile)
	r.Get("/account/sessions", func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if raw := r.URL.Query().Get("page"); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 {
				s.studyError(w, study.ErrInvalid)
				return
			}
			page = n
		}
		v, e := s.Study.Sessions(r.Context(), currentUser(r).ID, cookieToken(r, userCookie), page)
		s.respond(w, v, e)
	})
	r.Delete("/account/sessions/{public_id}", func(w http.ResponseWriter, r *http.Request) {
		s.respond(w, map[string]any{"ok": true}, s.Study.RevokeSession(r.Context(), currentUser(r).ID, cookieToken(r, userCookie), chi.URLParam(r, "public_id")))
	})
	r.Post("/account/sessions/revoke-others", func(w http.ResponseWriter, r *http.Request) {
		ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || ct != "application/json" {
			writeErr(w, http.StatusUnsupportedMediaType, "请求必须使用 application/json")
			return
		}
		v, e := s.Study.RevokeOthers(r.Context(), currentUser(r).ID, cookieToken(r, userCookie))
		s.respond(w, map[string]any{"revoked": v}, e)
	})
}

func (s *Server) accountDashboard(w http.ResponseWriter, r *http.Request) {
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			s.studyError(w, study.ErrInvalid)
			return
		}
		days = value
	}
	value, err := s.Study.Dashboard(r.Context(), currentUser(r).ID, days)
	s.respond(w, value, err)
}

func (s *Server) accountExport(w http.ResponseWriter, r *http.Request) {
	days := r.URL.Query().Get("days")
	if days == "" {
		days = "30"
	}
	value, err := s.Study.ExportCSV(r.Context(), currentUser(r).ID, days)
	if err != nil {
		s.studyError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="study-records.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(value)
}

func (s *Server) accountWrongbook(w http.ResponseWriter, r *http.Request) {
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || ct != "application/json" {
		writeErr(w, http.StatusUnsupportedMediaType, "请求必须使用 application/json")
		return
	}
	value, err := s.Study.PrioritizedWrongbook(r.Context(), currentUser(r).ID)
	s.respond(w, value, err)
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	var in study.ProfileUpdate
	var raw json.RawMessage
	if !decode(w, r, &raw) {
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		writeErr(w, http.StatusBadRequest, "资料必须为 JSON 对象")
		return
	}
	editableFields := []string{"nickname", "bio", "avatar_id", "daily_questions", "daily_minutes", "exam_name", "exam_date", "default_limit", "default_module", "reading_size", "revision"}
	if len(fields) != len(editableFields) {
		writeErr(w, http.StatusBadRequest, "请仅提交完整的可编辑资料字段")
		return
	}
	for _, key := range editableFields {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			writeErr(w, http.StatusBadRequest, "资料字段缺失: "+key)
			return
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "资料字段不合法: "+err.Error())
		return
	}
	v, e := s.Study.UpdateProfile(r.Context(), currentUser(r).ID, in)
	s.respond(w, v, e)
}

func deviceLabel(ua string) string {
	browser, os := "其他浏览器", "未知系统"
	switch {
	case strings.Contains(ua, "Edg/") || strings.Contains(ua, "EdgA/") || strings.Contains(ua, "EdgiOS/"):
		browser = "Edge"
	case strings.Contains(ua, "Firefox/") || strings.Contains(ua, "FxiOS/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/") || strings.Contains(ua, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}
	switch {
	case strings.Contains(ua, "Android"):
		os = "Android"
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad"):
		os = "iOS"
	case strings.Contains(ua, "Windows"):
		os = "Windows"
	case strings.Contains(ua, "Mac OS X"):
		os = "macOS"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	}
	return browser + " / " + os
}
