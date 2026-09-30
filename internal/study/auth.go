// Package study implements the online question bank and practice rules.
// HTTP handlers never contain SQL; all online writes are short transactions.
package study

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalid     = errors.New("请求参数不合法")
	ErrCredentials = errors.New("账号或密码错误")
	ErrConflict    = errors.New("用户名已被使用")
	ErrNotFound    = errors.New("记录不存在")
	ErrForbidden   = errors.New("请先作答后查看解析")
)

const SessionTTL = 7 * 24 * time.Hour

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("constant-timing-placeholder"), bcrypt.DefaultCost)

type Service struct{ DB *sql.DB }
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }
func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func validPassword(p string) bool { return len(p) >= 8 && len(p) <= 72 }
func (s *Service) Register(ctx context.Context, username, password, nickname string) (User, error) {
	username = strings.TrimSpace(username)
	nickname = strings.TrimSpace(nickname)
	if utf8.RuneCountInString(username) < 3 || utf8.RuneCountInString(username) > 32 || utf8.RuneCountInString(nickname) > 40 || !validPassword(password) {
		return User{}, errors.Join(ErrInvalid, errors.New("用户名需 3–32 个字符，密码需 8–72 字节，昵称最多 40 字"))
	}
	for _, c := range username {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return User{}, errors.Join(ErrInvalid, errors.New("用户名仅支持字母、数字、下划线和短横线"))
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO app_user(username,password_hash,nickname,created_at) VALUES(?,?,?,?)`, username, string(hash), nickname, now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, ErrConflict
		}
		return User{}, err
	}
	id, err := res.LastInsertId()
	return User{id, username, nickname}, err
}
func (s *Service) Login(ctx context.Context, username, password string) (string, User, error) {
	var u User
	var hash string
	err := s.DB.QueryRowContext(ctx, `SELECT id,username,nickname,password_hash FROM app_user WHERE username=?`, strings.TrimSpace(username)).Scan(&u.ID, &u.Username, &u.Nickname, &hash)
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
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", u, err
	}
	token := hex.EncodeToString(b)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", u, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM app_session WHERE expires_at<=?`, now()); err != nil {
		return "", u, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO app_session(token,user_id,expires_at) VALUES(?,?,?)`, tokenHash(token), u.ID, time.Now().UTC().Add(SessionTTL).Format(time.RFC3339)); err != nil {
		return "", u, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE app_user SET last_login_at=? WHERE id=?`, now(), u.ID); err != nil {
		return "", u, err
	}
	return token, u, tx.Commit()
}
func (s *Service) Verify(ctx context.Context, token string) (User, error) {
	var u User
	err := s.DB.QueryRowContext(ctx, `SELECT u.id,u.username,u.nickname FROM app_session s JOIN app_user u ON u.id=s.user_id WHERE s.token=? AND s.expires_at>?`, tokenHash(token), now()).Scan(&u.ID, &u.Username, &u.Nickname)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrCredentials
	}
	return u, err
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
		return ErrCredentials
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
