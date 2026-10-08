package study

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/netip"
	"strings"
	"time"

	"ai_analyze_guokao/internal/store"
)

type LoginMetadata struct {
	DeviceLabel string
	IP          string
}

type LoginSession struct {
	PublicID    string  `json:"public_id"`
	Current     bool    `json:"current"`
	DeviceLabel *string `json:"device_label"`
	IPHint      *string `json:"ip_hint"`
	CreatedAt   *string `json:"created_at"`
	ExpiresAt   string  `json:"expires_at"`
}

type SessionPage struct {
	Items []LoginSession `json:"items"`
	Total int            `json:"total"`
	Page  int            `json:"page"`
	Size  int            `json:"size"`
}

func newPublicID() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}

// All callers, including non-HTTP integrations, get bounded categories.
func controlledDevice(label string) string {
	parts := strings.Split(label, " / ")
	if len(parts) != 2 {
		return "信息未知"
	}
	allowedBrowser := map[string]bool{"Chrome": true, "Edge": true, "Firefox": true, "Safari": true, "其他浏览器": true}
	allowedOS := map[string]bool{"Windows": true, "macOS": true, "Linux": true, "Android": true, "iOS": true, "未知系统": true}
	if !allowedBrowser[parts[0]] || !allowedOS[parts[1]] {
		return "信息未知"
	}
	return label
}

func maskIP(raw string) string {
	ip, err := netip.ParseAddr(raw)
	if err != nil {
		return ""
	}
	ip = ip.Unmap()
	if ip.Is4() {
		value := ip.String()
		return value[:strings.LastIndex(value, ".")] + ".*"
	}
	return netip.PrefixFrom(ip, 48).Masked().String()
}

func (s *Service) Sessions(ctx context.Context, user int64, token string, page int) (SessionPage, error) {
	if page == 0 {
		page = 1
	}
	if page < 1 || page > 1000000 {
		return SessionPage{}, ErrInvalid
	}
	out := SessionPage{Items: []LoginSession{}, Page: page, Size: 20}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	stamp := s.clockNow().UTC().Format(time.RFC3339)
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_session WHERE user_id=? AND expires_at>?`, user, stamp).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT public_id,token=?,device_label,ip_hint,created_at,expires_at FROM app_session WHERE user_id=? AND expires_at>? ORDER BY created_at DESC,public_id LIMIT 20 OFFSET ?`, tokenHash(token), user, stamp, (page-1)*20)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var row LoginSession
		if err = rows.Scan(&row.PublicID, &row.Current, &row.DeviceLabel, &row.IPHint, &row.CreatedAt, &row.ExpiresAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, row)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Service) RevokeSession(ctx context.Context, user int64, token, id string) error {
	return store.WriteTx(ctx, s.DB, func(tx *sql.Tx) error {
		var current bool
		err := tx.QueryRowContext(ctx, `SELECT token=? FROM app_session WHERE user_id=? AND public_id=? AND expires_at>?`, tokenHash(token), user, id, s.clockNow().UTC().Format(time.RFC3339)).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if current {
			return errors.Join(ErrInvalid, errors.New("当前会话请使用退出登录"))
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM app_session WHERE user_id=? AND public_id=?`, user, id)
		return err
	})
}

func (s *Service) RevokeOthers(ctx context.Context, user int64, token string) (int64, error) {
	var count int64
	err := store.WriteTx(ctx, s.DB, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM app_session WHERE user_id=? AND token=? AND expires_at>?)`, user, tokenHash(token), s.clockNow().UTC().Format(time.RFC3339)).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrCredentials
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM app_session WHERE user_id=? AND token<>?`, user, tokenHash(token))
		if err != nil {
			return err
		}
		count, err = res.RowsAffected()
		return err
	})
	return count, err
}
