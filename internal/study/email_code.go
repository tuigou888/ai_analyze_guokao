package study

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"ai_analyze_guokao/internal/store"
)

// ErrTooFrequent 表示同一邮箱在重发窗口内重复请求验证码。
var ErrTooFrequent = errors.New("发送过于频繁，请稍后再试")

const (
	emailCodeTTL       = 5 * time.Minute  // 验证码有效期
	emailCodeResendGap = 60 * time.Second // 同一邮箱两次发送的最小间隔
)

// normalizeEmail 做注册与验证码共用的邮箱归一化：去首尾空格、转小写。
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// validEmail 是 RFC 5322 的简化校验：总长 ≤254、恰一个 @、本地部分与
// 域名非空、域名含点，且全串不含空白。不追求严格语法，只拒绝明显胡写。
func validEmail(email string) bool {
	if email == "" || len(email) > 254 || strings.ContainsAny(email, " \t\r\n") {
		return false
	}
	local, domain, found := strings.Cut(email, "@")
	if !found || local == "" || domain == "" || strings.Contains(domain, "@") {
		return false
	}
	return strings.Contains(domain, ".")
}

// CreateEmailCode 为邮箱生成 6 位数字验证码：哈希入库、5 分钟有效、
// 60 秒内同邮箱只能发一次，发新码时作废该邮箱所有未使用旧码。
// 明文验证码只随返回值交给调用方去发邮件，绝不落库。
func (s *Service) CreateEmailCode(ctx context.Context, email string) (id int64, code string, err error) {
	email = normalizeEmail(email)
	if !validEmail(email) {
		return 0, "", errors.Join(ErrInvalid, errors.New("邮箱格式不正确"))
	}
	now := s.clockNow().UTC()
	code, err = randomCode()
	if err != nil {
		return 0, "", err
	}
	err = store.WriteTx(ctx, s.DB, func(tx *sql.Tx) error {
		var last string
		e := tx.QueryRowContext(ctx, `SELECT created_at FROM email_code WHERE email=? ORDER BY id DESC LIMIT 1`, email).Scan(&last)
		if e == nil {
			if t, perr := time.Parse(time.RFC3339, last); perr == nil && now.Sub(t) < emailCodeResendGap {
				return ErrTooFrequent
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE email_code SET consumed_at=? WHERE email=? AND consumed_at IS NULL`, now.Format(time.RFC3339), email); e != nil {
			return e
		}
		res, e := tx.ExecContext(ctx, `INSERT INTO email_code(email,code_hash,expires_at,created_at) VALUES(?,?,?,?)`,
			email, tokenHash(code), now.Add(emailCodeTTL).Format(time.RFC3339), now.Format(time.RFC3339))
		if e != nil {
			return e
		}
		id, e = res.LastInsertId()
		return e
	})
	if err != nil {
		return 0, "", err
	}
	return id, code, nil
}

// DeleteEmailCode 删除一条验证码记录：发送失败时回收，避免未送达的码
// 占用 60 秒重发窗口。
func (s *Service) DeleteEmailCode(ctx context.Context, id int64) error {
	return store.WriteTx(ctx, s.DB, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `DELETE FROM email_code WHERE id=?`, id)
		return e
	})
}

func randomCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
