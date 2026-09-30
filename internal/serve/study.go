package serve

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"ai_analyze_guokao/internal/study"
	"github.com/go-chi/chi/v5"
)

const userCookie = "gk_user"

type userKey struct{}

func currentUser(r *http.Request) study.User {
	u, _ := r.Context().Value(userKey{}).(study.User)
	return u
}
func cookieToken(r *http.Request, name string) string {
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}
func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.Study.Verify(r.Context(), cookieToken(r, userCookie))
		if err != nil {
			if errors.Is(err, study.ErrCredentials) {
				writeErr(w, 401, "请先登录")
			} else {
				s.fail(w, "读取会话失败", err)
			}
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	})
}
func (s *Server) studyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, study.ErrInvalid):
		writeErr(w, 400, err.Error())
	case errors.Is(err, study.ErrCredentials):
		writeErr(w, 401, "账号或密码错误")
	case errors.Is(err, study.ErrConflict):
		writeErr(w, 409, err.Error())
	case errors.Is(err, study.ErrNotFound):
		writeErr(w, 404, err.Error())
	case errors.Is(err, study.ErrForbidden):
		writeErr(w, 403, err.Error())
	default:
		s.fail(w, "操作失败，请稍后重试", err)
	}
}
func (s *Server) respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		s.studyError(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func pathID(r *http.Request) int64        { v, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64); return v }
func num(r *http.Request, key string) int { v, _ := strconv.Atoi(r.URL.Query().Get(key)); return v }
func (s *Server) userRoutes(r chi.Router) {
	r.Post("/auth/register", s.register)
	r.Post("/auth/login", s.userLogin)
	r.Post("/auth/logout", s.userLogout)
	r.Group(func(r chi.Router) {
		r.Use(s.requireUser)
		r.Get("/auth/me", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, currentUser(r)) })
		r.Post("/auth/password", s.userPassword)
		r.Get("/filters", func(w http.ResponseWriter, r *http.Request) { v, e := s.Study.Filters(r.Context()); s.respond(w, v, e) })
		r.Get("/questions", func(w http.ResponseWriter, r *http.Request) {
			v, e := s.Study.Questions(r.Context(), study.FilterFrom(r.URL.Query()))
			s.respond(w, v, e)
		})
		r.Get("/questions/{id}", func(w http.ResponseWriter, r *http.Request) {
			v, e := s.Study.Question(r.Context(), pathID(r), false, 0)
			s.respond(w, v, e)
		})
		r.Get("/questions/{id}/reveal", func(w http.ResponseWriter, r *http.Request) {
			v, e := s.Study.Reveal(r.Context(), currentUser(r).ID, pathID(r))
			s.respond(w, v, e)
		})
		r.Get("/papers", func(w http.ResponseWriter, r *http.Request) {
			v, e := s.Study.Papers(r.Context(), study.FilterFrom(r.URL.Query()))
			s.respond(w, v, e)
		})
		r.Get("/concepts", func(w http.ResponseWriter, r *http.Request) {
			v, e := s.Study.Concepts(r.Context(), currentUser(r).ID, r.URL.Query().Get("module"), r.URL.Query().Get("secondary"))
			s.respond(w, v, e)
		})
		r.Get("/concepts/{id}", func(w http.ResponseWriter, r *http.Request) {
			v, e := s.Study.Concept(r.Context(), currentUser(r).ID, pathID(r))
			s.respond(w, v, e)
		})
		r.Get("/search", func(w http.ResponseWriter, r *http.Request) {
			f := study.FilterFrom(r.URL.Query())
			f.Size = num(r, "limit")
			v, e := s.Study.Search(r.Context(), f, r.URL.Query().Get("mode"))
			s.respond(w, v, e)
		})
		r.Post("/practice/sessions", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Kind string     `json:"kind"`
				Spec study.Spec `json:"spec"`
			}
			if !decode(w, r, &in) {
				return
			}
			v, e := s.Study.CreateSession(r.Context(), currentUser(r).ID, in.Kind, in.Spec)
			s.respond(w, v, e)
		})
		r.Get("/practice/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
			v, e := s.Study.Session(r.Context(), currentUser(r).ID, pathID(r))
			s.respond(w, v, e)
		})
		r.Put("/practice/sessions/{id}/answers", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Answers []study.Answer `json:"answers"`
			}
			if !decode(w, r, &in) {
				return
			}
			s.respond(w, map[string]any{"ok": true}, s.Study.Draft(r.Context(), currentUser(r).ID, pathID(r), in.Answers))
		})
		r.Post("/practice/sessions/{id}/submit", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Answers []study.Answer `json:"answers"`
			}
			if !decode(w, r, &in) {
				return
			}
			v, e := s.Study.Submit(r.Context(), currentUser(r).ID, pathID(r), in.Answers)
			s.respond(w, v, e)
		})
		r.Get("/practice/records", func(w http.ResponseWriter, r *http.Request) {
			v, e := s.Study.Records(r.Context(), currentUser(r).ID, num(r, "limit"), num(r, "offset"))
			s.respond(w, v, e)
		})
		r.Get("/practice/stats", func(w http.ResponseWriter, r *http.Request) {
			v, e := s.Study.Stats(r.Context(), currentUser(r).ID)
			s.respond(w, v, e)
		})
		r.Post("/practice/stats/rebuild", func(w http.ResponseWriter, r *http.Request) {
			s.respond(w, map[string]any{"ok": true}, s.Study.RebuildStats(r.Context(), currentUser(r).ID))
		})
		r.Get("/wrongbook", func(w http.ResponseWriter, r *http.Request) {
			v, e := s.Study.Wrongbook(r.Context(), currentUser(r).ID, num(r, "page"), num(r, "size"), r.URL.Query().Get("status"))
			s.respond(w, v, e)
		})
		r.Post("/wrongbook/{id}", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Action string `json:"action"`
			}
			if !decode(w, r, &in) {
				return
			}
			s.respond(w, map[string]any{"ok": true}, s.Study.WrongAction(r.Context(), currentUser(r).ID, pathID(r), in.Action))
		})
		r.Get("/favorites", func(w http.ResponseWriter, r *http.Request) {
			v, e := s.Study.Favorites(r.Context(), currentUser(r).ID, num(r, "page"), num(r, "size"))
			s.respond(w, v, e)
		})
		r.Post("/favorites/{id}", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Action string `json:"action"`
			}
			if !decode(w, r, &in) {
				return
			}
			s.respond(w, map[string]any{"ok": true}, s.Study.FavoriteAction(r.Context(), currentUser(r).ID, pathID(r), in.Action))
		})
	})
}
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Nickname string `json:"nickname"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, e := s.Study.Register(r.Context(), in.Username, in.Password, in.Nickname)
	if e != nil {
		s.studyError(w, e)
		return
	}
	writeJSON(w, 201, v)
}
func secure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
func (s *Server) userLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	token, u, e := s.Study.Login(r.Context(), in.Username, in.Password)
	if e != nil {
		s.studyError(w, e)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: userCookie, Value: token, Path: "/", HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode, MaxAge: int(study.SessionTTL.Seconds())})
	writeJSON(w, 200, u)
}
func (s *Server) userLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.Study.Logout(r.Context(), cookieToken(r, userCookie)); err != nil {
		s.studyError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: userCookie, Path: "/", HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) userPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current  string `json:"current_password"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.respond(w, map[string]any{"ok": true}, s.Study.Password(r.Context(), currentUser(r).ID, in.Current, in.Password))
}

// hostMatches 检查 Origin 与 Host 是否同源。
// 生产环境 Nginx 代理时 Origin 携带的是外网 Host，后端 127.0.0.1:8081 不匹配，
// 因此只比较主机名部分，忽略端口。开发环境 Vite(5173) 代理到 8081 亦可通过。
func hostMatches(originHost, reqHost string) bool {
	o := strings.ToLower(originHost)
	r := strings.ToLower(reqHost)
	// SplitHostPort  extracts hostname and port
	if h, _, err := net.SplitHostPort(o); err == nil {
		o = h
	}
	if h, _, err := net.SplitHostPort(r); err == nil {
		r = h
	}
	// localhost 与 127.0.0.1 视为等价，便于本机调试
	oIsLocal, rIsLocal := isLocalhost(o), isLocalhost(r)
	return o == r || (oIsLocal && rIsLocal)
}

func isLocalhost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// Reject cross-origin writes even when a browser sends authentication cookies.
// 开发环境下，浏览器会设置 Sec-Fetch-Site: cross-site 来区分严格同源（SameSite=None）
// 和宽松同源（SameSite=Lax 等）。Vite 代理到后端时，这会导致 POST 请求被误判。
// 因此在 Origin 可解析且匹配的情况下，直接放行，不依赖 Sec-Fetch-Site。
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			origin := r.Header.Get("Origin")
			if origin == "" {
				writeErr(w, 403, "不允许缺少 Origin 的跨站提交")
				return
			}
			u, err := url.Parse(origin)
			if err != nil || !hostMatches(u.Host, r.Host) {
				writeErr(w, 403, "不允许跨站提交")
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// Keep login/registration work bounded on the 2-core server. Client IP is taken
// from the TCP peer, never from an untrusted forwarded header.
type rateEntry struct {
	count int
	until time.Time
}
type authLimiter struct {
	mu      sync.Mutex
	entries map[string]rateEntry
	slots   chan struct{}
}

func newAuthLimiter() *authLimiter {
	return &authLimiter{entries: map[string]rateEntry{}, slots: make(chan struct{}, 2)}
}
func (l *authLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p != "/api/auth/login" && p != "/api/auth/register" && p != "/api/admin/login" {
			next.ServeHTTP(w, r)
			return
		}
		l.mu.Lock()
		t := time.Now()
		for k, v := range l.entries {
			if t.After(v.until) {
				delete(l.entries, k)
			}
		}
		key := r.RemoteAddr
		if i := strings.LastIndex(key, ":"); i >= 0 {
			key = key[:i]
		}
		e, exists := l.entries[key]
		if !exists && len(l.entries) >= 10000 {
			l.mu.Unlock()
			w.Header().Set("Retry-After", "60")
			writeErr(w, 429, "请求过于频繁，请稍后重试")
			return
		}
		if e.until.IsZero() {
			e.until = t.Add(time.Minute)
		}
		e.count++
		l.entries[key] = e
		blocked := e.count > 30
		l.mu.Unlock()
		if blocked {
			w.Header().Set("Retry-After", "60")
			writeErr(w, 429, "请求过于频繁，请稍后重试")
			return
		}
		select {
		case l.slots <- struct{}{}:
			defer func() { <-l.slots }()
			next.ServeHTTP(w, r)
		default:
			writeErr(w, 429, "登录繁忙，请稍后重试")
		}
	})
}
