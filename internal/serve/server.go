// Package serve 是 HTTP 服务层。
//
// 分层约定（见 docs/架构方案.md §8.7）：本包只负责 HTTP 语义（路由、鉴权、
// 序列化、状态码），业务规则在 internal/admin、internal/setting、internal/llm 里，
// SQL 一律不出现。
package serve

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"ai_analyze_guokao/internal/admin"
	"ai_analyze_guokao/internal/llm"
	"ai_analyze_guokao/internal/netguard"
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
	Assets         fs.FS
	Study          *study.Service
	DataDir        string
	TrustedProxies []netip.Prefix
	PublicOrigin   string
	// AllowPrivateOutbound 允许 LLM/SMTP 目标指向内网地址（自部署常见）。
	// 默认 false：管理端可改出站地址，若不加限制就成了内网探测原语（M-2）。
	AllowPrivateOutbound bool
}

// New 构造服务。
func New(settings *setting.Store, db *sql.DB, assets fs.FS) *Server {
	s := &Server{Settings: settings, DB: db, Log: slog.Default(), Assets: assets, Study: &study.Service{DB: db}}
	// 免费额度从配置读：解析失败或 ≤0 视作未配置，回落 study 默认。
	s.Study.Quota = func(ctx context.Context) (int, int) {
		positive := func(key string, fallback int) int {
			v, err := settings.Get(ctx, key)
			if err != nil {
				return fallback
			}
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || n <= 0 {
				return fallback
			}
			return n
		}
		return positive(setting.KeyDailyFreeQuestions, study.DefaultFreeQuestions),
			positive(setting.KeyDailyFreePapers, study.DefaultFreePapers)
	}
	return s
}

// Routes 组装路由。
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(s.sameOrigin)
	r.Use(newAuthLimiter(s.TrustedProxies...).middleware)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		// 只对回环/可信代理返回真实状态：503 与固定 200 的差异可用于探测
		// 后端可用性（安全审计 L-4）。外部访客一律 200，不泄露数据库状态。
		if !s.healthDetailAllowed(r) {
			writeJSON(w, 200, map[string]any{"ok": true})
			return
		}
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

		// 通用管理：operator 即可。
		r.Group(func(r chi.Router) {
			r.Use(s.requireAdmin)
			r.Get("/admin/me", s.handleMe)
			r.Post("/admin/concepts/refresh", func(w http.ResponseWriter, r *http.Request) {
				s.respond(w, map[string]any{"ok": true}, s.Study.RefreshConcepts(r.Context()))
			})
			r.Post("/admin/password", s.handleChangePassword)
			r.Get("/admin/users", s.handleAdminListUsers)
			r.Put("/admin/users/{id}/status", s.handleAdminSetUserStatus)
			r.Put("/admin/users/{id}/role", s.handleAdminSetUserRole)
			r.Put("/admin/users/{id}/password", s.handleAdminResetUserPassword)
		})
		// 超管专属：能看/改服务端配置、发探测请求。
		r.Group(func(r chi.Router) {
			r.Use(s.requireAdmin, s.requireRole)
			r.Get("/admin/settings", s.handleGetSettings)
			r.Put("/admin/settings", s.handlePutSettings)
			r.Post("/admin/test-llm", s.handleTestLLM)
			r.Post("/admin/test-mail", s.handleTestMail)
			r.Get("/admin/admins", s.handleAdminListAdmins)
			r.Post("/admin/admins", s.handleAdminCreate)
			r.Put("/admin/admins/{id}/password", s.handleAdminResetAdminPassword)
			r.Delete("/admin/admins/{id}", s.handleAdminDelete)
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

// adminRoleKey 承载管理员角色：requireAdmin 验证后放上下文，requireRole 与
// handleMe 读取。
type adminRoleKey struct{}

func currentAdminRole(r *http.Request) string {
	role, _ := r.Context().Value(adminRoleKey{}).(string)
	return role
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if token == "" {
			if c, err := r.Cookie(sessionCookie); err == nil {
				token = c.Value
			}
		}
		name, role, err := admin.Verify(r.Context(), s.DB, token)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		// 身份互斥：gk_admin 与 gk_user 同域共存，管理端权限不得在仍持有有效
		// 用户会话的浏览器上行使——否则"先登管理端、再登普通用户"的浏览器能以
		// 用户身份直接进管理台。Verify 报 ErrCredentials（无 cookie/过期/封禁）
		// 视为无用户身份放行；其余错误按故障关闭处理。
		if _, uerr := s.Study.Verify(r.Context(), cookieToken(r, userCookie)); uerr == nil {
			writeErr(w, http.StatusUnauthorized, "当前浏览器仍持有用户登录，请先退出用户身份")
			return
		} else if !errors.Is(uerr, study.ErrCredentials) {
			s.fail(w, "读取用户会话失败", uerr)
			return
		}
		r.Header.Set("X-Admin-User", name)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminRoleKey{}, role)))
	})
}

// requireRole 只放行超级管理员；必须挂在 requireAdmin 之后。
func (s *Server) requireRole(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if currentAdminRole(r) != admin.RoleSuper {
			writeErr(w, http.StatusForbidden, "该操作需要超级管理员权限")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// healthDetailAllowed 只对回环开放详细健康状态。两条部署路线的自检都走
// 本机回环（Compose healthcheck 在容器内 curl localhost；systemd 路线 curl
// 127.0.0.1），因此不需要放行可信代理网段——否则容器路线下所有经反代的
// 请求都会被判为"可信"，L-4 的收敛就失效了。
func (s *Server) healthDetailAllowed(r *http.Request) bool {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(h)
	if err != nil {
		return false
	}
	return ip.Unmap().IsLoopback()
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
	token, name, role, err := admin.Login(r.Context(), s.DB, in.Username, in.Password)
	if err != nil {
		if errors.Is(err, admin.ErrInvalidCredentials) {
			writeErr(w, http.StatusUnauthorized, "账号或密码错误")
			return
		}
		s.fail(w, "登录失败", err)
		return
	}
	// 身份切换：登管理端即吊销本浏览器携带的用户会话并清除其 cookie，保证
	// 一个浏览器同一时刻只有一个身份。与 requireAdmin 的互斥检查配套：即使
	// 这里吊销失败，管理接口仍会被互斥检查拒绝——失败方向是关闭的。
	if uc := cookieToken(r, userCookie); uc != "" {
		if err := s.Study.Logout(r.Context(), uc); err != nil {
			s.fail(w, "切换身份失败", err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: userCookie, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: int(admin.SessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": name, "role": role})
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
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: true, MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"username": r.Header.Get("X-Admin-User"), "role": currentAdminRole(r)})
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
// 未知键整批拒绝（安全审计 M-1）：配置表不接受任意键。
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if !decode(w, r, &in) {
		return
	}
	for k := range in {
		if !setting.IsKnownKey(k) {
			writeErr(w, http.StatusBadRequest, "未知的配置键: "+k)
			return
		}
	}
	// 出站目标校验（安全审计 M-2）：base_url / smtp_host 会被服务端真实请求，
	// 保存时先拒绝明显的回环/内网/保留地址；建连时还会按解析后的 IP 再判一次。
	for k, v := range in {
		if v == setting.Mask || v == "" {
			continue
		}
		if err := s.checkOutboundSetting(k, v); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
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
	// 建连时按解析后的 IP 再判一次，挡住"域名解析到内网"的绕过。
	if err := s.checkOutboundSetting(setting.KeyBaseURL, baseURL); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	client := llm.New(baseURL, apiKey, 0)
	client.HTTP.Transport = &http.Transport{
		DialContext:           netguard.ContextDialer(s.AllowPrivateOutbound, 30*time.Second),
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}
	respModel, latency, err := client.Ping(ctx, model)
	if err != nil {
		// 只回分类化文案：底层错误原文可能泄露内部主机名、网段与端口状态，
		// 是有效的内网扫描原语（安全审计 M-2）。原文仅写服务端日志。
		s.Log.Warn("test-llm 失败", "err", err, "base_url", baseURL)
		writeErr(w, http.StatusBadGateway, outboundErrorText(err))
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
		Current  string `json:"current_password"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	name := r.Header.Get("X-Admin-User")
	if err := admin.ChangePassword(r.Context(), s.DB, name, in.Current, in.Password); err != nil {
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
	return key == setting.KeyAPIKey || key == setting.KeySMTPPassword
}

// checkOutboundSetting 校验会被服务端主动请求的配置键（安全审计 M-2）。
// AllowPrivateOutbound 显式打开时跳过（自部署把 LLM/SMTP 放在内网的场景）。
func (s *Server) checkOutboundSetting(key, value string) error {
	if s.AllowPrivateOutbound {
		return nil
	}
	switch key {
	case setting.KeyBaseURL:
		return netguard.CheckURL(value)
	case setting.KeySMTPHost:
		return netguard.CheckHost(value)
	}
	return nil
}

// outboundErrorText 把底层错误压成不含内部拓扑的分类化文案。
func outboundErrorText(err error) string {
	if errors.Is(err, netguard.ErrForbiddenTarget) {
		return "目标地址指向内网或保留网段，已拒绝（如确需访问，请以 --allow-private-outbound 启动）"
	}
	var le *llm.Error
	if errors.As(err, &le) {
		if le.StatusCode > 0 {
			return fmt.Sprintf("LLM 网关返回 HTTP %d，请检查 API 地址、密钥与模型名", le.StatusCode)
		}
		return "无法连接 LLM 网关，请检查 API 地址与网络"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "LLM 网关响应超时"
	}
	return "LLM 连通性检查失败，详情见服务端日志"
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || ct != "application/json" {
		writeErr(w, 415, "请求必须使用 application/json")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		writeErr(w, 400, "请求体只能包含一个 JSON 对象")
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
