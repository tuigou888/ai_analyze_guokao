package study

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"ai_analyze_guokao/internal/store"
)

const adminUsersPageSize = 20

// AdminUser 是管理端用户列表行。角色按有效权益展示，vip_expires_at 原样保留
// 供管理员核对（NULL 落成空串）。
type AdminUser struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	Nickname     string `json:"nickname"`
	Role         string `json:"role"`
	Status       string `json:"status"`
	VipExpiresAt string `json:"vip_expires_at"`
	CreatedAt    string `json:"created_at"`
	LastLoginAt  string `json:"last_login_at"`
}

type AdminUserPage struct {
	Total int         `json:"total"`
	Users []AdminUser `json:"users"`
}

// escapeLike 转义 LIKE 通配符，配合 ESCAPE '\' 使用，避免 q 里的 %/_/\ 被当通配。
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// AdminListUsers 分页返回用户，page 从 1 起、每页 20，按 id 升序。
// status∈{active,banned}、role∈{free,vip}（role 按有效权益判定），q 模糊匹配
// username/nickname；非法筛选值 → ErrInvalid。
func (s *Service) AdminListUsers(ctx context.Context, page int, status, role, q string) (AdminUserPage, error) {
	var out AdminUserPage
	if page < 1 {
		page = 1
	}
	if status != "" && status != "active" && status != "banned" {
		return out, errors.Join(ErrInvalid, errors.New("状态筛选无效"))
	}
	if role != "" && role != "free" && role != "vip" {
		return out, errors.Join(ErrInvalid, errors.New("角色筛选无效"))
	}
	today := s.today()
	var where []string
	var args []any
	if status != "" {
		where = append(where, "status=?")
		args = append(args, status)
	}
	if role == "vip" {
		where = append(where, "(role='vip' AND (vip_expires_at IS NULL OR vip_expires_at>=?))")
		args = append(args, today)
	} else if role == "free" {
		where = append(where, "NOT(role='vip' AND (vip_expires_at IS NULL OR vip_expires_at>=?))")
		args = append(args, today)
	}
	if q != "" {
		pattern := "%" + escapeLike(q) + "%"
		where = append(where, `(username LIKE ? ESCAPE '\' OR nickname LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_user`+cond, args...).Scan(&out.Total); err != nil {
		return AdminUserPage{}, err
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id,username,nickname,role,status,COALESCE(vip_expires_at,''),COALESCE(created_at,''),COALESCE(last_login_at,'') FROM app_user`+
			cond+` ORDER BY id ASC LIMIT ? OFFSET ?`,
		append(args, adminUsersPageSize, (page-1)*adminUsersPageSize)...)
	if err != nil {
		return AdminUserPage{}, err
	}
	defer rows.Close()
	out.Users = []AdminUser{}
	for rows.Next() {
		var u AdminUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Nickname, &u.Role, &u.Status, &u.VipExpiresAt, &u.CreatedAt, &u.LastLoginAt); err != nil {
			return AdminUserPage{}, err
		}
		u.Role = effectiveRole(u.Role, u.VipExpiresAt, today)
		out.Users = append(out.Users, u)
	}
	return out, rows.Err()
}

// AdminSetUserStatus 设置 active/banned；同值幂等成功。置 banned 时同事务清除
// 该用户全部会话（即封即踢）。目标不存在 → ErrNotFound。
func (s *Service) AdminSetUserStatus(ctx context.Context, id int64, status string) error {
	if status != "active" && status != "banned" {
		return errors.Join(ErrInvalid, errors.New("状态必须是 active 或 banned"))
	}
	return store.WriteTx(ctx, s.DB, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE app_user SET status=? WHERE id=?`, status, id)
		if err != nil {
			return err
		}
		if err := rowsFound(res); err != nil {
			return err
		}
		if status == "banned" {
			_, err = tx.ExecContext(ctx, `DELETE FROM app_session WHERE user_id=?`, id)
		}
		return err
	})
}

// AdminSetUserRole 设置 free/vip。vip 时空 expires 表示长期（NULL），非空须
// YYYY-MM-DD 且晚于考试时区的今天；free 一并清空到期日。
func (s *Service) AdminSetUserRole(ctx context.Context, id int64, role, expires string) error {
	if role != "vip" && role != "free" {
		return errors.Join(ErrInvalid, errors.New("角色必须是 free 或 vip"))
	}
	if role == "vip" && expires != "" {
		day, err := time.Parse("2006-01-02", expires)
		if err != nil || day.Format("2006-01-02") != expires || expires <= s.today() {
			return errors.Join(ErrInvalid, errors.New("到期日期格式应为 YYYY-MM-DD 且晚于今天"))
		}
	}
	return store.WriteTx(ctx, s.DB, func(tx *sql.Tx) error {
		var res sql.Result
		var err error
		if role == "vip" {
			var value any
			if expires != "" {
				value = expires
			}
			res, err = tx.ExecContext(ctx, `UPDATE app_user SET role='vip', vip_expires_at=? WHERE id=?`, value, id)
		} else {
			res, err = tx.ExecContext(ctx, `UPDATE app_user SET role='free', vip_expires_at=NULL WHERE id=?`, id)
		}
		if err != nil {
			return err
		}
		return rowsFound(res)
	})
}

// AdminResetUserPassword 用 bcrypt 重置用户密码并吊销其全部会话。不记录密码本身。
func (s *Service) AdminResetUserPassword(ctx context.Context, id int64, password string) error {
	if !validPassword(password) {
		return errors.Join(ErrInvalid, errors.New("新密码需 8–72 字节"))
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return store.WriteTx(ctx, s.DB, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE app_user SET password_hash=? WHERE id=?`, string(hash), id)
		if err != nil {
			return err
		}
		if err := rowsFound(res); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM app_session WHERE user_id=?`, id)
		return err
	})
}

// rowsFound 把 0 行受影响翻译成 ErrNotFound（SQLite 同值 UPDATE 仍计 1 行，
// 因此 0 行只可能是目标不存在）。
func rowsFound(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
