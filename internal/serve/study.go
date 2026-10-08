package serve

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"ai_analyze_guokao/internal/store"
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
		if !expectedUserMatches(w, r, u.ID) {
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	})
}
func (s *Server) studyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrWriteBusy):
		w.Header().Set("Retry-After", "1")
		writeErr(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, study.ErrInvalid):
		writeErr(w, 400, err.Error())
	case errors.Is(err, study.ErrCredentials):
		writeErr(w, 401, "账号或密码错误")
	case errors.Is(err, study.ErrConflict), errors.Is(err, study.ErrDraftConflict), errors.Is(err, study.ErrProfileConflict):
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
		s.accountRoutes(r)
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
				Answers  []study.Answer `json:"answers"`
				Revision *int64         `json:"draft_revision"`
			}
			if !decode(w, r, &in) {
				return
			}
			if in.Revision == nil || *in.Revision < 0 {
				s.studyError(w, study.ErrInvalid)
				return
			}
			revision, e := s.Study.Draft(r.Context(), currentUser(r).ID, pathID(r), in.Answers, *in.Revision)
			s.respond(w, map[string]any{"ok": true, "draft_revision": revision}, e)
		})
		r.Post("/practice/sessions/{id}/submit", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Answers  []study.Answer `json:"answers"`
				Revision *int64         `json:"draft_revision"`
			}
			if !decode(w, r, &in) {
				return
			}
			if in.Revision == nil || *in.Revision < 0 {
				s.studyError(w, study.ErrInvalid)
				return
			}
			v, e := s.Study.Submit(r.Context(), currentUser(r).ID, pathID(r), in.Answers, *in.Revision)
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
	return r.TLS != nil
}
func (s *Server) userLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	token, u, e := s.Study.LoginWithMetadata(r.Context(), in.Username, in.Password, study.LoginMetadata{DeviceLabel: deviceLabel(r.UserAgent()), IP: clientIP(r, s.TrustedProxies)})
	if e != nil {
		s.studyError(w, e)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: userCookie, Value: token, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: int(study.SessionTTL.Seconds())})
	writeJSON(w, 200, u)
}
func (s *Server) userLogout(w http.ResponseWriter, r *http.Request) {
	// Legacy logout remains idempotent. A page identity precondition first needs
	// the same verified principal boundary as other authenticated operations.
	if _, present := r.Header[http.CanonicalHeaderKey("X-GK-Expected-User")]; present {
		s.requireUser(http.HandlerFunc(s.logoutVerifiedUser)).ServeHTTP(w, r)
		return
	}
	s.logoutVerifiedUser(w, r)
}
func (s *Server) logoutVerifiedUser(w http.ResponseWriter, r *http.Request) {
	if err := s.Study.Logout(r.Context(), cookieToken(r, userCookie)); err != nil {
		s.studyError(w, err)
		return
	}
	// Guarded pages revoke this token without a delayed response deleting a
	// Cookie issued by a later login. Legacy logout keeps explicit deletion.
	if _, guarded := r.Header[http.CanonicalHeaderKey("X-GK-Expected-User")]; !guarded {
		http.SetCookie(w, &http.Cookie{Name: userCookie, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	}
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
