package serve

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type rateEntry struct {
	count int
	until time.Time
}
type authLimiter struct {
	mu      sync.Mutex
	entries map[string]rateEntry
	slots   chan struct{}
	trusted []netip.Prefix
}

func newAuthLimiter(trusted ...netip.Prefix) *authLimiter {
	return &authLimiter{entries: map[string]rateEntry{}, slots: make(chan struct{}, 2), trusted: trusted}
}
func trustedIP(ip netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}

// Walk from the TCP peer toward the client; never take the untrusted leftmost hop.
func clientIP(r *http.Request, prefixes []netip.Prefix) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		h = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(h)
	if err != nil {
		return "unknown"
	}
	peer = peer.Unmap()
	if !trustedIP(peer, prefixes) {
		return peer.String()
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			return peer.String()
		}
		ip = ip.Unmap()
		if !trustedIP(ip, prefixes) || i == 0 {
			return ip.String()
		}
	}
	return peer.String()
}
func (l *authLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p != "/api/auth/login" && p != "/api/auth/register" && p != "/api/admin/login" {
			next.ServeHTTP(w, r)
			return
		}
		keys := []string{"ip:" + clientIP(r, l.trusted)}
		if r.Body != nil {
			raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
			r.Body.Close()
			if err != nil {
				writeErr(w, 400, "请求体过大或读取失败")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var in struct {
				Username string `json:"username"`
			}
			if json.Unmarshal(raw, &in) == nil {
				if name := strings.TrimSpace(in.Username); name != "" {
					digest := sha256.Sum256([]byte(name))
					keys = append(keys, "account:"+p+":"+hex.EncodeToString(digest[:]))
				}
			}
		}
		l.mu.Lock()
		t := time.Now()
		for k, v := range l.entries {
			if !t.Before(v.until) {
				delete(l.entries, k)
			}
		}
		blocked := false
		for _, key := range keys {
			e := l.entries[key]
			if e.until.IsZero() {
				if len(l.entries) >= 8192 {
					blocked = true
					continue
				}
				e.until = t.Add(time.Minute)
			}
			e.count++
			l.entries[key] = e
			if e.count > 30 {
				blocked = true
			}
		}
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
