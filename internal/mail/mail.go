// Package mail 是最小 SMTP 客户端：只负责把一封纯文本邮件交给指定的
// SMTP 服务器，不依赖数据库——配置由调用方加载后传入，便于独立测试。
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Config 是一次发送所需的全部服务器信息。
type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

// Configured 判断是否已配置：主机、端口与发件人缺一不可。
func (c Config) Configured() bool {
	return c.Host != "" && c.Port != "" && c.From != ""
}

// ErrNotConfigured 表示未配置 SMTP，调用方据此走开发降级路径。
var ErrNotConfigured = errors.New("SMTP 未配置")

// smtpTimeout 是整个 SMTP 会话（问候、认证、DATA、QUIT）的读写截止时间，
// 防止服务器接受连接后装死拖住调用方。测试中可调小。
var smtpTimeout = 15 * time.Second

// Send 用给定配置同步发送一封纯文本邮件。
//
// 端口 465 走隐式 TLS；其余端口先明文连接，服务器支持时升级 STARTTLS。
// 需要认证而链路始终未加密时直接拒绝——认证凭据不允许在明文上发送。
func Send(ctx context.Context, cfg Config, to, subject, body string) error {
	if !cfg.Configured() {
		return ErrNotConfigured
	}
	d := &net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(cfg.Host, cfg.Port))
	if err != nil {
		return fmt.Errorf("连接 SMTP 服务器失败: %w", err)
	}
	rawConn := conn
	defer conn.Close()
	if err := rawConn.SetDeadline(time.Now().Add(smtpTimeout)); err != nil {
		return fmt.Errorf("设置 SMTP 超时失败: %w", err)
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			rawConn.Close()
		case <-done:
		}
	}()
	if cfg.Port == "465" {
		conn = tls.Client(conn, &tls.Config{ServerName: cfg.Host})
	}
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if _, ok := client.TLSConnectionState(); !ok {
		if hasTLS, _ := client.Extension("STARTTLS"); hasTLS {
			if err = client.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
				return fmt.Errorf("STARTTLS 升级失败: %w", err)
			}
		}
	}
	if cfg.Username != "" {
		if _, ok := client.TLSConnectionState(); !ok {
			return errors.New("SMTP 服务器不支持加密连接，拒绝在明文链路上发送认证")
		}
		if err = client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	if err = client.Mail(cfg.From); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write([]byte(message(cfg.From, to, subject, body))); err != nil {
		w.Close()
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// message 拼装 RFC 5322 邮件：头 + 空行 + 正文，行结束一律 CRLF。
func message(from, to, subject, body string) string {
	headers := []string{
		"From: " + from,
		"To: " + to,
		"Subject: " + mime.BEncoding.Encode("UTF-8", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Date: " + time.Now().Format(time.RFC1123Z),
	}
	var b strings.Builder
	for _, h := range headers {
		b.WriteString(h)
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	b.WriteString("\r\n")
	return b.String()
}
