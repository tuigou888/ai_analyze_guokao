package serve

import (
	"errors"
	"fmt"
	"net/http"

	"ai_analyze_guokao/internal/admin"
	"ai_analyze_guokao/internal/study"
)

// adminUsersError 把 study 域错误翻成用户管理接口的 HTTP 语义：
// 目标不存在统一 404「用户不存在」，其余回落通用 studyError。
func (s *Server) adminUsersError(w http.ResponseWriter, err error) {
	if errors.Is(err, study.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	s.studyError(w, err)
}

// auditUser 记录一条用户管理审计。审计失败只写服务日志，不影响已成功的业务响应。
func (s *Server) auditUser(r *http.Request, action string, id int64, detail string) {
	target := fmt.Sprintf("user:%d", id)
	if err := admin.Audit(r.Context(), s.DB, r.Header.Get("X-Admin-User"), action, target, detail); err != nil {
		s.Log.Error("写审计失败", "action", action, "target", target, "err", err)
	}
}

// handleAdminListUsers GET /api/admin/users?page&status&role&q
func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	page, err := s.Study.AdminListUsers(r.Context(), num(r, "page"), query.Get("status"), query.Get("role"), query.Get("q"))
	if err != nil {
		s.adminUsersError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handleAdminSetUserStatus PUT /api/admin/users/{id}/status
func (s *Server) handleAdminSetUserStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := pathID(r)
	if err := s.Study.AdminSetUserStatus(r.Context(), id, in.Status); err != nil {
		s.adminUsersError(w, err)
		return
	}
	action, detail := "user_ban", `{"status":"banned"}`
	if in.Status == "active" {
		action, detail = "user_unban", `{"status":"active"}`
	}
	s.auditUser(r, action, id, detail)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminSetUserRole PUT /api/admin/users/{id}/role
func (s *Server) handleAdminSetUserRole(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role    string `json:"role"`
		Expires string `json:"expires"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := pathID(r)
	if err := s.Study.AdminSetUserRole(r.Context(), id, in.Role, in.Expires); err != nil {
		s.adminUsersError(w, err)
		return
	}
	if in.Role == "vip" {
		detail := "长期"
		if in.Expires != "" {
			detail = fmt.Sprintf(`{"expires":%q}`, in.Expires)
		}
		s.auditUser(r, "vip_set", id, detail)
	} else {
		s.auditUser(r, "vip_clear", id, `{"expires":""}`)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminResetUserPassword PUT /api/admin/users/{id}/password
func (s *Server) handleAdminResetUserPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := pathID(r)
	if err := s.Study.AdminResetUserPassword(r.Context(), id, in.Password); err != nil {
		s.adminUsersError(w, err)
		return
	}
	s.auditUser(r, "user_password_reset", id, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
