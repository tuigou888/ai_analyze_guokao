package mail

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// smtpRecorder 记录假 SMTP 服务器收到的每一行（命令与 DATA 内容）。
type smtpRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *smtpRecorder) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
}

func (r *smtpRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

// fakeSMTP 起一个最小 SMTP 服务器：不广告 STARTTLS，不要求认证，
// 对所有命令回 250。返回端口与记录器。
func fakeSMTP(t *testing.T) (port string, rec *smtpRecorder) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	rec = &smtpRecorder{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakeSMTP(conn, rec)
		}
	}()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port), rec
}

func serveFakeSMTP(conn net.Conn, rec *smtpRecorder) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	writeLine := func(s string) {
		w.WriteString(s + "\r\n")
		w.Flush()
	}
	writeLine("220 test ESMTP")
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		rec.add(line)
		if inData {
			if line == "." {
				inData = false
				writeLine("250 OK")
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			writeLine("250-test ESMTP")
			writeLine("250 OK")
		case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
			writeLine("250 OK")
		case line == "DATA":
			writeLine("354 go ahead")
			inData = true
		case line == "QUIT":
			writeLine("221 bye")
			return
		default:
			writeLine("250 OK")
		}
	}
}

func TestSendNotConfigured(t *testing.T) {
	if err := Send(context.Background(), Config{}, "a@b.co", "主题", "正文"); !errors.Is(err, ErrNotConfigured) {
		t.Fatal(err)
	}
}

func TestSendPlain(t *testing.T) {
	port, rec := fakeSMTP(t)
	cfg := Config{Host: "127.0.0.1", Port: port, From: "noreply@example.com"}
	if err := Send(context.Background(), cfg, "to@example.com", "行测研习注册验证码", "你的验证码是 123456，5 分钟内有效。"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(rec.all(), "\n")
	lower := strings.ToLower(joined)
	for _, want := range []string{
		"mail from:<noreply@example.com>",
		"rcpt to:<to@example.com>",
		"subject: =?utf-8?b?", // RFC 2047 编码标记大小写不敏感
		"content-type: text/plain; charset=utf-8",
		"你的验证码是 123456，5 分钟内有效。",
	} {
		if !strings.Contains(lower, want) {
			t.Fatalf("SMTP 流中缺少 %q:\n%s", want, joined)
		}
	}
}

func TestSendRefusesAuthWithoutTLS(t *testing.T) {
	port, _ := fakeSMTP(t)
	cfg := Config{Host: "127.0.0.1", Port: port, Username: "u", Password: "p", From: "noreply@example.com"}
	err := Send(context.Background(), cfg, "to@example.com", "主题", "正文")
	if err == nil || !strings.Contains(err.Error(), "SMTP 服务器不支持加密连接") {
		t.Fatal(err)
	}
}

// silentSMTP 起一个假 SMTP 服务器：接受连接、发 220 问候后对一切命令保持沉默。
func silentSMTP(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				c.Write([]byte("220 silent ESMTP\r\n"))
				io.Copy(io.Discard, c)
			}(conn)
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

// 没有任何 ctx 取消时，沉默的 SMTP 服务器也不能拖住 Send 超过 smtpTimeout。
func TestSendTimesOutOnSilentServer(t *testing.T) {
	old := smtpTimeout
	smtpTimeout = 200 * time.Millisecond
	t.Cleanup(func() { smtpTimeout = old })
	port := silentSMTP(t)
	cfg := Config{Host: "127.0.0.1", Port: port, From: "noreply@example.com"}
	done := make(chan error, 1)
	go func() { done <- Send(context.Background(), cfg, "to@example.com", "主题", "正文") }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected timeout error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Send blocked on silent SMTP server")
	}
}

// 请求 ctx 取消后，仍阻塞在 SMTP 会话中的 Send 必须及时返回。
func TestSendHonorsContextCancellation(t *testing.T) {
	port := silentSMTP(t)
	cfg := Config{Host: "127.0.0.1", Port: port, From: "noreply@example.com"}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Send(ctx, cfg, "to@example.com", "主题", "正文") }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error after ctx cancellation")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Send blocked despite ctx cancellation")
	}
}
