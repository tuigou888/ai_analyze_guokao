package serve

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

func NormalizeOrigin(value string) (string, error) {
	u, e := url.Parse(value)
	if e != nil || u == nil {
		return "", fmt.Errorf("无效 Origin")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("Origin 必须为 http(s)://主机[:端口]")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(host, port), nil
}
func sameOrigin(next http.Handler) http.Handler { return (&Server{}).sameOrigin(next) }
func (s *Server) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			expected := s.PublicOrigin
			if expected == "" {
				scheme := "http"
				if r.TLS != nil {
					scheme = "https"
				} else {
					h, _, _ := net.SplitHostPort(r.RemoteAddr)
					ip, e := netip.ParseAddr(h)
					if e == nil && trustedIP(ip, s.TrustedProxies) && r.Header.Get("X-Forwarded-Proto") == "https" {
						scheme = "https"
					}
				}
				expected = scheme + "://" + r.Host
			}
			a, e := NormalizeOrigin(r.Header.Get("Origin"))
			b, e2 := NormalizeOrigin(expected)
			if e != nil || e2 != nil || a != b {
				writeErr(w, 403, "不允许跨站提交或缺少有效 Origin")
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
