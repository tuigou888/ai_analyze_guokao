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

// contentSecurityPolicy 是页面级纵深防御。前端的唯一 HTML 注入点是
// RichText.vue 的 v-html（先转义文本、图片路径走白名单、KaTeX trust:false），
// 当前无可利用 XSS；CSP 的作用是未来新增渲染点时兜底。
//
// style-src 必须带 'unsafe-inline'：Vue 组件的 style 绑定与 KaTeX 都以内联
// style 属性输出。script-src 只允许 'self'：构建产物是外链 module，无内联脚本。
const contentSecurityPolicy = "default-src 'self'; " +
	"img-src 'self' data:; " +
	"style-src 'self' 'unsafe-inline'; " +
	"font-src 'self' data:; " +
	"script-src 'self'; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'"

// requestIsHTTPS 判断浏览器侧是否为 HTTPS：直连 TLS，或来自可信代理且
// X-Forwarded-Proto 声明 https。HSTS 只在 HTTPS 响应上才有意义。
func requestIsHTTPS(r *http.Request, trusted []netip.Prefix) bool {
	if r.TLS != nil {
		return true
	}
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip, e := netip.ParseAddr(h)
	return e == nil && trustedIP(ip, trusted) && r.Header.Get("X-Forwarded-Proto") == "https"
}

func sameOrigin(next http.Handler) http.Handler { return (&Server{}).sameOrigin(next) }
func (s *Server) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			expected := s.PublicOrigin
			if expected == "" {
				scheme := "http"
				if requestIsHTTPS(r, s.TrustedProxies) {
					scheme = "https"
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
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=()")
		if requestIsHTTPS(r, s.TrustedProxies) {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
