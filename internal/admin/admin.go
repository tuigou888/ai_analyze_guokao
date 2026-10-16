// Package admin 提供运维入口的鉴权。
//
// 与产品用户体系分开：这是"能改服务端配置"的运维身份，权限远大于普通用户，
// 因此不共用账号表，也不允许自助注册——只能由命令行创建。
package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// SessionTTL 会话有效期。运维入口权限大，不宜过长。
const SessionTTL = 12 * time.Hour

// 管理员角色：super 可做一切与管理员管理；operator 只做被放行的运维操作。
const (
	RoleSuper    = "super"
	RoleOperator = "operator"
)

// ErrInvalidCredentials 账号或口令错误。刻意不区分二者，避免账号枚举。
var ErrInvalidCredentials = errors.New("账号或密码错误")

// ErrNoAdmin 还没有任何管理员账号。
var ErrNoAdmin = errors.New("尚未创建管理员账号，请先执行 gk admin create")

var (
	// ErrDuplicate 用户名撞 UNIQUE 冲突。
	ErrDuplicate = errors.New("用户名已存在")
	// ErrRoleInvalid 角色不是 super/operator。
	ErrRoleInvalid = errors.New("角色必须是 super 或 operator")
	// ErrSelfDelete 试图删除当前登录账号。
	ErrSelfDelete = errors.New("不能删除当前登录账号")
	// ErrLastSuper 试图删掉最后一个超级管理员。
	ErrLastSuper = errors.New("至少保留一个超级管理员")
	// ErrNoTarget 不存在被操作的目标管理员。
	ErrNoTarget = errors.New("目标管理员不存在")
)

// ValidRole 判断 r 是否为合法管理员角色。
func ValidRole(r string) bool {
	return r == RoleSuper || r == RoleOperator
}

// Ensure 在不存在任何管理员时创建初始账号（幂等）。
func Ensure(ctx context.Context, db *sql.DB, username, password string) (created bool, err error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_user`).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	return true, Create(ctx, db, username, password, RoleSuper)
}

// Create 创建管理员。role 只能是 super 或 operator。口令要求至少 8 位——它是
// 唯一能改服务端配置的凭据。
func Create(ctx context.Context, db *sql.DB, username, password, role string) error {
	if !ValidRole(role) {
		return ErrRoleInvalid
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("用户名不能为空")
	}
	if len(password) < 8 {
		return errors.New("密码至少 8 位")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx,
		`INSERT INTO admin_user(username, password_hash, role, created_at) VALUES (?,?,?,?)`,
		username, string(hash), role, time.Now().Format(time.RFC3339)); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrDuplicate
		}
		return err
	}
	return nil
}

// SetPassword 改口令，并吊销该管理员的所有现有会话——改密码后旧会话继续有效
// 是常见的安全疏漏。
func SetPassword(ctx context.Context, db *sql.DB, username, password string) error {
	return setPassword(ctx, db, username, password, "")
}

// ChangePassword is the browser-facing path; CLI recovery retains SetPassword.
func ChangePassword(ctx context.Context, db *sql.DB, username, current, password string) error {
	var hash string
	if err := db.QueryRowContext(ctx, `SELECT password_hash FROM admin_user WHERE username=?`, username).Scan(&hash); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return ErrInvalidCredentials
	}
	return setPassword(ctx, db, username, password, hash)
}

func setPassword(ctx context.Context, db *sql.DB, username, password, expectedHash string) error {
	if len(password) < 8 {
		return errors.New("密码至少 8 位")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE admin_user SET password_hash=? WHERE username=? AND (?='' OR password_hash=?)`,
		string(hash), strings.TrimSpace(username), expectedHash, expectedHash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrInvalidCredentials
	}
	_, err = tx.ExecContext(ctx,
		`DELETE FROM admin_session WHERE admin_id = (SELECT id FROM admin_user WHERE username=?)`,
		strings.TrimSpace(username))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Login 校验口令并签发会话 token，返回 token 与会话身份（用户名、角色）。
func Login(ctx context.Context, db *sql.DB, username, password string) (string, string, string, error) {
	var (
		id   int64
		hash string
		name string
		role string
	)
	err := db.QueryRowContext(ctx,
		`SELECT id, password_hash, username, role FROM admin_user WHERE username=?`, strings.TrimSpace(username)).
		Scan(&id, &hash, &name, &role)
	if errors.Is(err, sql.ErrNoRows) {
		// 仍然做一次 bcrypt 比较，抹平"账号不存在"与"密码错误"的耗时差。
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinvali"), []byte(password))
		return "", "", "", ErrInvalidCredentials
	}
	if err != nil {
		return "", "", "", err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return "", "", "", ErrInvalidCredentials
	}

	token, err := newToken()
	if err != nil {
		return "", "", "", err
	}
	res, err := db.ExecContext(ctx, `INSERT INTO admin_session(token, admin_id, created_at, expires_at) SELECT ?,id,?,? FROM admin_user WHERE id=? AND password_hash=?`, tokenHash(token), time.Now().Format(time.RFC3339), time.Now().UTC().Add(SessionTTL).Format(time.RFC3339), id, hash)
	if err != nil {
		return "", "", "", err
	}
	if n, err := res.RowsAffected(); err != nil {
		return "", "", "", err
	} else if n != 1 {
		return "", "", "", ErrInvalidCredentials
	}
	_, _ = db.ExecContext(ctx, `UPDATE admin_user SET last_login_at=? WHERE id=?`, time.Now().Format(time.RFC3339), id)
	return token, name, role, nil
}

// Verify 校验会话 token，返回管理员用户名与角色。过期的会话会被清理。
func Verify(ctx context.Context, db *sql.DB, token string) (string, string, error) {
	if token == "" {
		return "", "", ErrInvalidCredentials
	}
	var (
		adminID int64
		expires string
		name    string
		role    string
	)
	err := db.QueryRowContext(ctx,
		`SELECT s.admin_id, s.expires_at, u.username, u.role
		   FROM admin_session s JOIN admin_user u ON u.id = s.admin_id
		  WHERE s.token = ?`, tokenHash(token)).Scan(&adminID, &expires, &name, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrInvalidCredentials
	}
	if err != nil {
		return "", "", err
	}
	exp, perr := time.Parse(time.RFC3339, expires)
	if perr != nil || time.Now().After(exp) {
		_, _ = db.ExecContext(ctx, `DELETE FROM admin_session WHERE token=?`, tokenHash(token))
		return "", "", ErrInvalidCredentials
	}
	return name, role, nil
}

// Logout 删除会话。
func Logout(ctx context.Context, db *sql.DB, token string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM admin_session WHERE token=?`, tokenHash(token))
	return err
}

// PurgeExpired 清理过期会话。
func PurgeExpired(ctx context.Context, db *sql.DB) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM admin_session WHERE expires_at < ?`,
		time.Now().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Count 返回管理员数量，供启动时判断是否需要引导创建。
func Count(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_user`).Scan(&n)
	return n, err
}

// CountByRole 返回指定角色的管理员数量。
func CountByRole(ctx context.Context, db *sql.DB, role string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_user WHERE role=?`, role).Scan(&n)
	return n, err
}

// Audit 追加一条管理操作审计。admin_audit 只追加，不更新不改。
func Audit(ctx context.Context, db *sql.DB, admin, action, target, detail string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO admin_audit(admin, action, target, detail, created_at) VALUES (?,?,?,?,?)`,
		admin, action, target, detail, time.Now().UTC().Format(time.RFC3339))
	return err
}

// AdminRow 管理员列表行。
type AdminRow struct {
	ID          int64
	Username    string
	Role        string
	CreatedAt   string
	LastLoginAt string
}

// ListAdmins 返回全部管理员，按 id 升序。空时间列（NULL）落成空串。
func ListAdmins(ctx context.Context, db *sql.DB) ([]AdminRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, username, role, COALESCE(created_at,''), COALESCE(last_login_at,'')
		   FROM admin_user ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminRow
	for rows.Next() {
		var r AdminRow
		if err := rows.Scan(&r.ID, &r.Username, &r.Role, &r.CreatedAt, &r.LastLoginAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetPasswordByID 按管理员 id 改口令，并吊销该管理员的所有现有会话。
func SetPasswordByID(ctx context.Context, db *sql.DB, id int64, password string) error {
	if len(password) < 8 {
		return errors.New("密码至少 8 位")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE admin_user SET password_hash=? WHERE id=?`, string(hash), id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNoTarget
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_session WHERE admin_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Delete 删除管理员：不允许删自己；不允许删掉最后一个超管；目标不存在报
// ErrNoTarget。成功时连带吊销目标管理员的所有会话。
func Delete(ctx context.Context, db *sql.DB, id int64, currentName string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var username, role string
	err = tx.QueryRowContext(ctx, `SELECT username, role FROM admin_user WHERE id=?`, id).Scan(&username, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoTarget
	}
	if err != nil {
		return err
	}
	if username == strings.TrimSpace(currentName) {
		return ErrSelfDelete
	}
	if role == RoleSuper {
		var supers int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_user WHERE role=?`, RoleSuper).Scan(&supers); err != nil {
			return err
		}
		if supers <= 1 {
			return ErrLastSuper
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_session WHERE admin_id=?`, id); err != nil {
		return err
	}
	if res, err := tx.ExecContext(ctx, `DELETE FROM admin_user WHERE id=?`, id); err != nil {
		return err
	} else if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNoTarget
	}
	return tx.Commit()
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// tokenHash 与 study 层保持一致：哈希后再入库，从库泄露无法直接得到可用的会话。
func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
