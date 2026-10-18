// Package study implements the online question bank and practice rules.
// HTTP handlers never contain SQL; all online writes are short transactions.
package study

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"ai_analyze_guokao/internal/store"
)

var (
	ErrInvalid       = errors.New("请求参数不合法")
	ErrCredentials   = errors.New("账号或密码错误")
	ErrConflict      = errors.New("用户名已被使用")
	ErrNotFound      = errors.New("记录不存在")
	ErrForbidden     = errors.New("请先作答后查看解析")
	ErrDraftConflict = errors.New("练习已在其他窗口更新，请加载最新草稿后再保存或提交")
	ErrEmailConflict = errors.New("该邮箱已被注册")
	ErrBanned        = errors.New("账号已封禁，如有疑问请联系管理员")
)

const SessionTTL = 7 * 24 * time.Hour

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("constant-timing-placeholder"), bcrypt.DefaultCost)

type Service struct {
	DB    *sql.DB
	Clock func() time.Time
}
type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	Nickname     string `json:"nickname"`
	Role         string `json:"role"`
	VipExpiresAt string `json:"vip_expires_at,omitempty"`
}

var ExamZone = time.FixedZone("Asia/Shanghai", 8*60*60)

func (s *Service) today() string {
	return s.clockNow().In(ExamZone).Format("2006-01-02")
}

func effectiveRole(role, expires, today string) string {
	if role == "vip" && expires != "" && expires < today {
		return "free"
	}
	return role
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }
func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func validPassword(p string) bool { return len(p) >= 8 && len(p) <= 72 }
func (s *Service) Register(ctx context.Context, username, password, nickname, email, code string) (User, error) {
	username = strings.TrimSpace(username)
	nickname = strings.TrimSpace(nickname)
	email = normalizeEmail(email)
	if utf8.RuneCountInString(username) < 3 || utf8.RuneCountInString(username) > 32 || utf8.RuneCountInString(nickname) > 40 || !validPassword(password) {
		return User{}, errors.Join(ErrInvalid, errors.New("用户名需 3–32 个字符，密码需 8–72 字节，昵称最多 40 字"))
	}
	for _, c := range username {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return User{}, errors.Join(ErrInvalid, errors.New("用户名仅支持字母、数字、下划线和短横线"))
		}
	}
	if !validEmail(email) {
		return User{}, errors.Join(ErrInvalid, errors.New("邮箱格式不正确"))
	}
	codeHash := tokenHash(code)
	nowStr := s.clockNow().UTC().Format(time.RFC3339)
	var codeID int64
	var codeHashStored string
	var attempts int
	err := store.WriteTx(ctx, s.DB, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT id,code_hash,attempts FROM email_code WHERE email=? AND consumed_at IS NULL ORDER BY id DESC LIMIT 1`, email)
		if err := row.Scan(&codeID, &codeHashStored, &attempts); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.Join(ErrInvalid, errors.New("验证码错误或已过期"))
			}
			return err
		}
		var expiresAt string
		if err := tx.QueryRowContext(ctx, `SELECT expires_at FROM email_code WHERE id=?`, codeID).Scan(&expiresAt); err != nil {
			return err
		}
		if nowStr >= expiresAt {
			return errors.Join(ErrInvalid, errors.New("验证码错误或已过期"))
		}
		if attempts >= 5 {
			return errors.Join(ErrInvalid, errors.New("验证码错误次数过多，请重新获取"))
		}
		if subtle.ConstantTimeCompare([]byte(codeHash), []byte(codeHashStored)) != 1 {
			return errWrongCode
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errWrongCode) {
			if e := s.bumpAttempts(ctx, codeID); e != nil {
				return User{}, e
			}
			var count int
			_ = s.DB.QueryRowContext(ctx, `SELECT attempts FROM email_code WHERE id=?`, codeID).Scan(&count)
			if count >= 5 {
				return User{}, errors.Join(ErrInvalid, errors.New("验证码错误次数过多，请重新获取"))
			}
			return User{}, errors.Join(ErrInvalid, errors.New("验证码错误或已过期"))
		}
		return User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	var id int64
	err = store.WriteTx(ctx, s.DB, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_user WHERE email=?`, email).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return ErrEmailConflict
		}
		res, e := tx.ExecContext(ctx, `INSERT INTO app_user(username,password_hash,nickname,created_at,email) VALUES(?,?,?,?,?)`, username, string(hash), nickname, nowStr, email)
		if e != nil {
			if strings.Contains(e.Error(), "UNIQUE") && strings.Contains(e.Error(), "email") {
				return ErrEmailConflict
			}
			if strings.Contains(e.Error(), "UNIQUE") {
				return ErrConflict
			}
			return e
		}
		id, e = res.LastInsertId()
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO user_profile(user_id,updated_at) VALUES(?,?)`, id, nowStr)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE email_code SET consumed_at=? WHERE id=?`, nowStr, codeID)
		return e
	})
	if err != nil {
		return User{}, err
	}
	return User{ID: id, Username: username, Nickname: nickname, Role: "free"}, nil
}

var errWrongCode = errors.New("wrong code")

func (s *Service) bumpAttempts(ctx context.Context, codeID int64) error {
	return store.WriteTx(ctx, s.DB, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE email_code SET attempts=attempts+1 WHERE id=?`, codeID)
		return err
	})
}
func (s *Service) Login(ctx context.Context, username, password string) (string, User, error) {
	return s.LoginWithMetadata(ctx, username, password, LoginMetadata{})
}
func (s *Service) LoginWithMetadata(ctx context.Context, username, password string, metadata LoginMetadata) (string, User, error) {
	var u User
	var hash, status, role string
	var expires sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id,username,nickname,password_hash,status,role,vip_expires_at FROM app_user WHERE username=?`, strings.TrimSpace(username)).Scan(&u.ID, &u.Username, &u.Nickname, &hash, &status, &role, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return "", u, ErrCredentials
	}
	if err != nil {
		return "", u, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", User{}, ErrCredentials
	}
	if status == "banned" {
		return "", User{}, ErrBanned
	}
	u.Role = effectiveRole(role, expires.String, s.today())
	u.VipExpiresAt = expires.String
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", u, err
	}
	token := hex.EncodeToString(b)
	publicID, err := newPublicID()
	if err != nil {
		return "", u, err
	}
	timestamp := s.clockNow().UTC()
	var device, ip any
	if metadata.DeviceLabel != "" {
		device = controlledDevice(metadata.DeviceLabel)
	}
	if hint := maskIP(metadata.IP); hint != "" {
		ip = hint
	}
	err = store.WriteTx(ctx, s.DB, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `DELETE FROM app_session WHERE expires_at<=?`, timestamp.Format(time.RFC3339)); e != nil {
			return e
		}
		res, e := tx.ExecContext(ctx, `INSERT INTO app_session(token,user_id,expires_at,public_id,created_at,device_label,ip_hint) SELECT ?,id,?,?,?,?,? FROM app_user WHERE id=? AND password_hash=?`, tokenHash(token), timestamp.Add(SessionTTL).Format(time.RFC3339), publicID, timestamp.Format(time.RFC3339), device, ip, u.ID, hash)
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrCredentials
		}
		_, e = tx.ExecContext(ctx, `UPDATE app_user SET last_login_at=? WHERE id=?`, timestamp.Format(time.RFC3339), u.ID)
		return e
	})
	if err != nil {
		return "", User{}, err
	}
	return token, u, nil
}
func (s *Service) Verify(ctx context.Context, token string) (User, error) {
	var u User
	var status, role string
	var expires sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT u.id,u.username,u.nickname,u.status,u.role,u.vip_expires_at FROM app_session s JOIN app_user u ON u.id=s.user_id WHERE s.token=? AND s.expires_at>?`, tokenHash(token), s.clockNow().UTC().Format(time.RFC3339)).Scan(&u.ID, &u.Username, &u.Nickname, &status, &role, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrCredentials
	}
	if err != nil {
		return u, err
	}
	if status == "banned" {
		return User{}, ErrCredentials
	}
	u.Role = effectiveRole(role, expires.String, s.today())
	u.VipExpiresAt = expires.String
	return u, nil
}
func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM app_session WHERE token=?`, tokenHash(token))
	return err
}
func (s *Service) Password(ctx context.Context, id int64, current, password string) error {
	if !validPassword(password) {
		return errors.Join(ErrInvalid, errors.New("新密码需 8–72 字节"))
	}
	var hash string
	if err := s.DB.QueryRowContext(ctx, `SELECT password_hash FROM app_user WHERE id=?`, id).Scan(&hash); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return errors.Join(ErrInvalid, errors.New("原密码错误"))
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE app_user SET password_hash=? WHERE id=? AND password_hash=?`, string(h), id, hash)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCredentials
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM app_session WHERE user_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
