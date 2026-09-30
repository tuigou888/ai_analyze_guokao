// Package serve 是 HTTP 服务层。
//
// 分层约定（见 docs/架构方案.md §8.7）：本包只负责 HTTP 语义（路由、鉴权、
// 序列化、状态码），业务规则在 internal/admin、internal/setting、internal/llm 里，
// SQL 一律不出现。
package serve

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"ai_analyze_guokao/internal/admin"
	"ai_analyze_guokao/internal/llm"
	"ai_analyze_guokao/internal/setting"
	"ai_analyze_guokao/internal/study"
)

// Server 持有服务依赖。
//
// DB 只作为连接句柄传入：SQL 语句全部待在 internal/admin、internal/setting 里，
// serve 只做 HTTP 语义（路由、鉴权、序列化、状态码）。
type Server struct {
	Settings *setting.Store
	DB       *sql.DB
	Log      *slog.Logger
	// Assets 是前端构建产物（web/dist）。未注入时为 nil，根路径给出提示页。
	Assets  fs.FS
	Study   *study.Service
	DataDir string
}

// New 构造服务。
func New(settings *setting.Store, db *sql.DB, assets fs.FS) *Server {
	return &Server{Settings: settings, DB: db, Log: slog.Default(), Assets: assets, Study: &study.Service{DB: db}}
}

// Routes 组装路由。
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(sameOrigin)
	r.Use(newAuthLimiter().middleware)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.DB.PingContext(r.Context()); err != nil {
			writeErr(w, 503, "数据库不可用")
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	r.Route("/api", func(r chi.Router) {
		s.userRoutes(r)
		r.NotFound(func(w http.ResponseWriter, r *http.Request) { writeErr(w, 404, "接口不存在") })
		r.Post("/admin/login", s.handleLogin)
		r.Post("/admin/logout", s.handleLogout)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAdmin)
			r.Get("/admin/me", s.handleMe)
			r.Post("/admin/concepts/refresh", func(w http.ResponseWriter, r *http.Request) {
				s.respond(w, map[string]any{"ok": true}, s.Study.RefreshConcepts(r.Context()))
			})
			r.Get("/admin/settings", s.handleGetSettings)
			r.Put("/admin/settings", s.handlePutSettings)
			r.Post("/admin/test-llm", s.handleTestLLM)
			r.Post("/admin/password", s.handleChangePassword)
		})
	})

	// 前端构建产物（Vue 3）由 embed 注入；未注入时给一个说明页，
	// 避免开发阶段访问根路径看到 404 而误判服务没起来。
	r.With(s.requireUser).Get("/media/*", s.handleMedia)
	r.Get("/*", s.handleStatic)
	return r
}

// ---- 中间件 ----

const sessionCookie = "gk_admin"

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if token == "" {
			if c, err := r.Cookie(sessionCookie); err == nil {
				token = c.Value
			}
		}
		name, err := admin.Verify(r.Context(), s.DB, token)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		r.Header.Set("X-Admin-User", name)
		next.ServeHTTP(w, r)
	})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

// ---- 处理函数 ----

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	token, err := admin.Login(r.Context(), s.DB, in.Username, in.Password)
	if err != nil {
		if errors.Is(err, admin.ErrInvalidCredentials) {
			writeErr(w, http.StatusUnauthorized, "账号或密码错误")
			return
		}
		s.fail(w, "登录失败", err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode, MaxAge: int(admin.SessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"username": in.Username})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := bearer(r)
	if token == "" {
		if c, err := r.Cookie(sessionCookie); err == nil {
			token = c.Value
		}
	}
	if err := admin.Logout(r.Context(), s.DB, token); err != nil {
		s.fail(w, "登出失败", err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"username": r.Header.Get("X-Admin-User")})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	all, err := s.Settings.All(r.Context())
	if err != nil {
		s.fail(w, "读取配置失败", err)
		return
	}
	// All 已对敏感值做遮蔽，这里再确认一次不应出现明文密钥。
	writeJSON(w, http.StatusOK, map[string]any{"settings": all, "secret_mask": setting.Mask})
}

// handlePutSettings 批量更新配置。
//
// 关键行为：值为掩码（•）或 undefined 的敏感项**保持原值**。
// 否则管理界面每次保存都会把遮蔽显示写回去，把真实 key 抹成乱码。
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if !decode(w, r, &in) {
		return
	}
	by := r.Header.Get("X-Admin-User")
	updated := make([]string, 0, len(in))
	for k, v := range in {
		if v == setting.Mask || v == "" && isSecretSetting(k) {
			continue // 未修改，保持原值
		}
		if err := s.Settings.Set(r.Context(), k, v, by); err != nil {
			s.fail(w, "保存配置失败: "+k, err)
			return
		}
		updated = append(updated, k)
	}
	all, err := s.Settings.All(r.Context())
	if err != nil {
		s.fail(w, "读取配置失败", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": updated, "settings": all})
}

// handleTestLLM 用当前配置打一次真实请求，验证连通性并核对模型名。
func (s *Server) handleTestLLM(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	baseURL, _ := s.Settings.Get(ctx, setting.KeyBaseURL)
	apiKey, _ := s.Settings.Get(ctx, setting.KeyAPIKey)
	model, _ := s.Settings.Get(ctx, setting.KeyModel)

	if baseURL == "" || model == "" {
		writeErr(w, http.StatusBadRequest, "请先填写 API 地址与模型名")
		return
	}
	client := llm.New(baseURL, apiKey, 0)
	respModel, latency, err := client.Ping(ctx, model)
	if err != nil {
		// 把不可重试的配置类错误直接透传，便于按提示修正
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}

	// 配置名 vs 回执名：不一致说明网关做了路由改写，会直接影响成本与质量口径（§3.7）。
	warning := ""
	if respModel != "" && respModel != model {
		warning = "配置的模型名与回执中的模型名不一致，请确认网关路由"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "model_config": model, "model_response": respModel,
		"latency_ms": latency.Milliseconds(), "warning": warning,
	})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	name := r.Header.Get("X-Admin-User")
	if err := admin.SetPassword(r.Context(), s.DB, name, in.Password); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"note": "密码已更新，所有旧会话已被吊销，请重新登录",
	})
}

// ---- 工具 ----

func isSecretSetting(key string) bool {
	return key == setting.KeyAPIKey
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func (s *Server) fail(w http.ResponseWriter, msg string, err error) {
	s.Log.Error(msg, "err", err)
	writeErr(w, http.StatusInternalServerError, msg)
}
