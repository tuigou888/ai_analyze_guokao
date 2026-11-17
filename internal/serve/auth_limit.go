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

// 限速设计（审计 H-1 / H-2 的修复）：
//
//   - **按接口独立分桶**：登录、注册、管理端登录、验证码互不影响。以往它们共用
//     一份 IP 键，任一接口被打满其余接口一并 429，匿名者即可让全站无法登录。
//   - **账号维度与 IP 维度分开**：账号桶防单账号撞库，IP 桶防同一来源横扫。
//     两者不叠加计数，不会互相放大。
//   - **只对失败计数，成功后清零**：正常流量不该消耗预算；成功登录还能立刻
//     解除该账号的失败累计（受害者被恶意锁定时可自助恢复）。
//   - **并发闸门按接口独立**，不再是全站 2 个槽。
const authWindow = time.Minute

// authGroup 是一类接口的限速参数。
type authGroup struct {
	accountLimit int // 单账号（或单邮箱）预算/窗口；0 表示不做账号桶
	ipLimit      int // 单来源 IP 预算/窗口
	slots        int // 并发处理上限（保护 bcrypt 等 CPU 密集路径）
	// countAll 表示按请求计数而非只计失败。验证码发送成功也会产生真实成本
	// （邮件额度、收件方打扰），不能等失败才限。
	countAll bool
}

var authGroups = map[string]authGroup{
	"/api/auth/login":      {accountLimit: 10, ipLimit: 30, slots: 8},
	"/api/auth/register":   {accountLimit: 10, ipLimit: 20, slots: 8},
	"/api/admin/login":     {accountLimit: 10, ipLimit: 30, slots: 8},
	"/api/auth/email-code": {accountLimit: 5, ipLimit: 30, slots: 4, countAll: true},
}

type rateEntry struct {
	count int
	until time.Time
}

// bucket 是一个固定窗口计数器集合；窗口从首次失败起算。
type bucket struct {
	mu      sync.Mutex
	limit   int
	entries map[string]rateEntry
	// maxEntries 防止伪造键把内存撑爆（键已哈希，长度可控，但数量仍需设上限）。
	maxEntries int
}

func newBucket(limit int) *bucket {
	return &bucket{limit: limit, entries: map[string]rateEntry{}, maxEntries: 8192}
}

// blocked 在窗口内是否已达上限（不计数）。
func (b *bucket) blocked(key string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[key]
	if !ok || !now.Before(e.until) {
		return false
	}
	return e.count >= b.limit
}

// fail 记一次失败。窗口过期后重新起算。
func (b *bucket) fail(key string, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[key]
	if !ok || !now.Before(e.until) {
		if len(b.entries) >= b.maxEntries {
			b.pruneLocked(now)
			if len(b.entries) >= b.maxEntries {
				// 表满时拒绝新增键：让最老的键继续生效，避免攻击者用海量新键
				// 冲刷掉正在封禁的条目。
				if _, exists := b.entries[key]; !exists {
					return
				}
			}
		}
		e = rateEntry{until: now.Add(authWindow)}
	}
	e.count++
	b.entries[key] = e
}

// reset 成功后清零，让正常用户不必等窗口结束。
func (b *bucket) reset(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, key)
}

func (b *bucket) pruneLocked(now time.Time) {
	for k, v := range b.entries {
		if !now.Before(v.until) {
			delete(b.entries, k)
		}
	}
}

type authLimiter struct {
	groups  map[string]authGroup
	account map[string]*bucket // 按接口隔离的账号桶
	ip      map[string]*bucket // 按接口隔离的 IP 桶
	slots   map[string]chan struct{}
	trusted []netip.Prefix
	now     func() time.Time
}

func newAuthLimiter(trusted ...netip.Prefix) *authLimiter {
	l := &authLimiter{
		groups:  authGroups,
		account: map[string]*bucket{},
		ip:      map[string]*bucket{},
		slots:   map[string]chan struct{}{},
		trusted: trusted,
		now:     time.Now,
	}
	for path, g := range l.groups {
		l.account[path] = newBucket(g.accountLimit)
		l.ip[path] = newBucket(g.ipLimit)
		l.slots[path] = make(chan struct{}, g.slots)
	}
	return l
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

// statusRecorder 只用于判定处理结果，不影响写回。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = 200
	}
	return r.ResponseWriter.Write(b)
}

func (l *authLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		g, ok := l.groups[p]
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		ipKey := "ip:" + clientIP(r, l.trusted)
		var accountKeys []string
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
				Email    string `json:"email"`
			}
			if json.Unmarshal(raw, &in) == nil {
				for _, v := range []string{strings.TrimSpace(in.Username), strings.TrimSpace(strings.ToLower(in.Email))} {
					if v == "" {
						continue
					}
					digest := sha256.Sum256([]byte(v))
					accountKeys = append(accountKeys, hex.EncodeToString(digest[:]))
				}
			}
		}

		now := l.now()
		if l.ip[p].blocked(ipKey, now) {
			w.Header().Set("Retry-After", "60")
			writeErr(w, 429, "请求过于频繁，请稍后重试")
			return
		}
		for _, k := range accountKeys {
			if l.account[p].blocked(k, now) {
				w.Header().Set("Retry-After", "60")
				writeErr(w, 429, "请求过于频繁，请稍后重试")
				return
			}
		}

		select {
		case l.slots[p] <- struct{}{}:
			defer func() { <-l.slots[p] }()
		default:
			w.Header().Set("Retry-After", "1")
			writeErr(w, 429, "请求繁忙，请稍后重试")
			return
		}

		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		// 计数口径：
		//   - 登录/注册/管理端登录：只把 4xx 认证失败计入预算，成功后清零账号桶
		//     （IP 桶保持，防止一次合法登录把扫描预算重置掉）。
		//   - 验证码发送：成功也要计数——每封邮件都有真实成本。
		if g.countAll {
			if rec.status >= 200 && rec.status < 300 {
				for _, k := range accountKeys {
					l.account[p].fail(k, l.now())
				}
				l.ip[p].fail(ipKey, l.now())
			}
			return
		}
		if rec.status == http.StatusUnauthorized || rec.status == http.StatusBadRequest ||
			rec.status == http.StatusForbidden || (rec.status >= 500 && rec.status != http.StatusBadGateway) {
			for _, k := range accountKeys {
				l.account[p].fail(k, l.now())
			}
			l.ip[p].fail(ipKey, l.now())
		} else if rec.status == http.StatusOK || rec.status == http.StatusCreated {
			for _, k := range accountKeys {
				l.account[p].reset(k)
			}
		}
	})
}
