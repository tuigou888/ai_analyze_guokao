package serve

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

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

// adminUsernamePattern 管理员用户名规则：3-32 位字母、数字、下划线、短横线。
var adminUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

// adminPasswordValid 密码 8–72 字节。上限必须前置拦：bcrypt 对 >72 字节直接报错，
// 放进去会变成 500 而不是 400。
func adminPasswordValid(pw string) bool {
	return len(pw) >= 8 && len(pw) <= 72
}

// adminManageError 把 admin 域错误翻成管理员管理接口的 HTTP 语义，优先级从具体到一般。
func (s *Server) adminManageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrNoTarget):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, admin.ErrDuplicate):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, admin.ErrSelfDelete), errors.Is(err, admin.ErrLastSuper), errors.Is(err, admin.ErrRoleInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		s.fail(w, "操作失败，请稍后重试", err)
	}
}

// auditAdmin 记录一条管理员管理审计。审计失败只写服务日志，不影响已成功的业务响应。
func (s *Server) auditAdmin(r *http.Request, action string, id int64, detail string) {
	target := fmt.Sprintf("admin:%d", id)
	if err := admin.Audit(r.Context(), s.DB, r.Header.Get("X-Admin-User"), action, target, detail); err != nil {
		s.Log.Error("写审计失败", "action", action, "target", target, "err", err)
	}
}

// adminRowJSON 是管理员列表的响应 DTO：admin.AdminRow 没有 json tag，
// serve 层把键名钉成小写下划线，保持前端契约稳定。
type adminRowJSON struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Role        string `json:"role"`
	CreatedAt   string `json:"created_at"`
	LastLoginAt string `json:"last_login_at"`
}

// handleAdminListAdmins GET /api/admin/admins
func (s *Server) handleAdminListAdmins(w http.ResponseWriter, r *http.Request) {
	rows, err := admin.ListAdmins(r.Context(), s.DB)
	if err != nil {
		s.fail(w, "读取管理员列表失败", err)
		return
	}
	out := make([]adminRowJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, adminRowJSON{
			ID: row.ID, Username: row.Username, Role: row.Role,
			CreatedAt: row.CreatedAt, LastLoginAt: row.LastLoginAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"admins": out})
}

// handleAdminCreate POST /api/admin/admins
func (s *Server) handleAdminCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	username := strings.TrimSpace(in.Username)
	if !adminUsernamePattern.MatchString(username) {
		writeErr(w, http.StatusBadRequest, "用户名需为 3-32 位字母、数字、下划线或短横线")
		return
	}
	if !adminPasswordValid(in.Password) {
		writeErr(w, http.StatusBadRequest, "密码需为 8-72 位")
		return
	}
	if err := admin.Create(r.Context(), s.DB, username, in.Password, in.Role); err != nil {
		s.adminManageError(w, err)
		return
	}
	// admin.Create 不回传新 id：按 username 反查唯一行，供审计 target 用。
	rows, err := admin.ListAdmins(r.Context(), s.DB)
	if err != nil {
		s.Log.Error("反查新管理员失败", "username", username, "err", err)
	} else {
		for _, row := range rows {
			if row.Username == username {
				s.auditAdmin(r, "admin_create", row.ID, fmt.Sprintf(`{"role":%q}`, in.Role))
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminResetAdminPassword PUT /api/admin/admins/{id}/password
func (s *Server) handleAdminResetAdminPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !adminPasswordValid(in.Password) {
		writeErr(w, http.StatusBadRequest, "密码需为 8-72 位")
		return
	}
	id := pathID(r)
	if err := admin.SetPasswordByID(r.Context(), s.DB, id, in.Password); err != nil {
		s.adminManageError(w, err)
		return
	}
	s.auditAdmin(r, "admin_password_reset", id, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminDelete DELETE /api/admin/admins/{id}
func (s *Server) handleAdminDelete(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := admin.Delete(r.Context(), s.DB, id, r.Header.Get("X-Admin-User")); err != nil {
		s.adminManageError(w, err)
		return
	}
	s.auditAdmin(r, "admin_delete", id, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
