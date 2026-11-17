// Package netguard 限制服务端主动发起的出站连接能够到达哪些地址。
//
// 管理端可以把 llm.base_url / mail.smtp_host 改成任意值，随后通过
// /api/admin/test-llm、/api/admin/test-mail 触发服务端真实请求。若不设限，
// 超级管理员（或窃得其凭据者）就能把它当成内网探测原语（安全审计 M-2）。
//
// 两道防线：
//   - CheckHost 在保存配置时拒绝字面量形式的危险地址（回环、链路本地、
//     私有网段、组播等）；
//   - DialControl 在真正建连时按**解析后的 IP** 再判一次，挡住 DNS 重绑定
//     与"域名解析到内网"的绕过。
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrForbiddenTarget 表示目标地址落在被禁止的网段。
var ErrForbiddenTarget = errors.New("目标地址指向内网或保留网段，不允许访问")

// extraForbidden 是 netip 内置判定之外的保留网段。
var extraForbidden = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // 本网络
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF 协议分配
	netip.MustParsePrefix("198.18.0.0/15"), // 基准测试
	netip.MustParsePrefix("240.0.0.0/4"),   // 保留
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64
}

// IsForbidden 判断一个已解析的 IP 是否属于不应由服务端主动访问的范围。
func IsForbidden(ip netip.Addr) bool {
	if !ip.IsValid() {
		return true
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, p := range extraForbidden {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// CheckHost 校验主机名/字面量 IP 是否可以指向该目标。主机名不做 DNS 解析：
// 解析结果随时间可变，真正的裁决在 DialControl。这里只拦"一眼就是内网"的写法。
func CheckHost(host string) error {
	host = strings.TrimSpace(host)
	// net/url 的 Hostname() 会去掉方括号，但直接配置 smtp_host 时可能带。
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if host == "" {
		return errors.New("目标主机为空")
	}
	if ip, err := netip.ParseAddr(host); err == nil && IsForbidden(ip) {
		return fmt.Errorf("%w: %s", ErrForbiddenTarget, host)
	}
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return fmt.Errorf("%w: %s", ErrForbiddenTarget, host)
	}
	return nil
}

// CheckURL 校验一个 http(s) URL 的主机部分。
func CheckURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil {
		return errors.New("目标地址格式不正确")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("目标地址必须是 http 或 https")
	}
	if u.User != nil {
		return errors.New("目标地址不允许包含用户名密码")
	}
	return CheckHost(u.Hostname())
}

// DialControl 返回 net.Dialer.Control 回调：在连接建立前拿到已解析的地址，
// 若落在禁止网段则拒绝。allowPrivate 为 true 时跳过检查（自部署场景显式放行）。
func DialControl(allowPrivate bool) func(network, address string, c syscall.RawConn) error {
	if allowPrivate {
		return nil
	}
	return func(network, address string, _ syscall.RawConn) error {
		h, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(h)
		if err != nil {
			return fmt.Errorf("无法解析目标地址 %q", h)
		}
		if IsForbidden(ip) {
			return fmt.Errorf("%w: %s", ErrForbiddenTarget, ip)
		}
		return nil
	}
}

// Dialer 构造带守护的 Dialer，供 http.Client 使用。
func Dialer(allowPrivate bool, timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, Control: DialControl(allowPrivate)}
}

// ContextDialer 是 mail.Send 需要的拨号函数形态。
func ContextDialer(allowPrivate bool, timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return Dialer(allowPrivate, timeout).DialContext
}
