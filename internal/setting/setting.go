// Package setting 存运行时配置，让管理入口能改 API 地址、密钥、模型等，
// 而不是只能改环境变量。
//
// 两条安全约束：
//   - 敏感值（API key、管理员口令）**加密后存库**，明文不落盘。
//   - 读取接口默认**遮蔽**敏感值，只在明确的内部调用点才解密——避免密钥顺着
//     某个"返回全部配置"的接口漏出去。
package setting

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 配置键。集中定义，避免各处拼字符串拼错。
const (
	KeyBaseURL      = "llm.base_url"
	KeyAPIKey       = "llm.api_key"
	KeyModel        = "llm.model"
	KeyModelPricing = "llm.price_per_1k" // 可选，"输入价,输出价"（美元/千 token）
	KeyConcurrency  = "llm.concurrency"
	KeyTimeoutSec   = "llm.timeout_sec"
	// KeyMaxTokens 单次生成上限。它不只影响花费：网关常在约 100 秒处掐断长请求
	// （Cloudflare 524），输出越长越容易撞上，所以这个值同时也是"超时"的间接旋钮。
	KeyMaxTokens = "llm.max_tokens"
	// KeyMaxRetries 单题最大重试次数。端点限流频繁时它决定成败：
	// 按单次成功率 p 计，重试 n 次后成功率为 1-(1-p)^(n+1)。
	// 实测某网关单次成功率约 60%，故默认 4（→ 约 99%）。
	KeyMaxRetries  = "llm.max_retries"
	KeyDailyBudget = "llm.daily_budget_usd" // AI 成本闸门（§8.6 约束 6）
)

// Mask 是敏感值在读取接口里的占位显示。用固定长度，不泄漏真实长度。
const Mask = "••••••••"

// Defaults 是各键的默认值（不含密钥）。
var Defaults = map[string]string{
	KeyBaseURL:     "https://new.951357.xyz/v1",
	KeyModel:       "deepseek-flash",
	KeyConcurrency: "5",
	KeyTimeoutSec:  "180",
	KeyMaxTokens:   "2500",
	KeyMaxRetries:  "4",
}

// Store 读写配置。
type Store struct {
	db  *sql.DB
	key []byte // AES-256 密钥
}

// Open 打开配置仓库。密钥来源优先 env GK_SECRET_KEY（hex 或任意口令，会做一次
// SHA-256 拉伸），否则用 <keyFile>；两者都没有就生成一份 0600 的密钥文件。
//
// 不把密钥硬编码在代码里：那等于没有加密。也不强制要求 env，否则首次启动
// 会直接起不来——对自部署的产品，落一份 0600 的本地密钥是务实的折中。
func Open(db *sql.DB, keyFile string) (*Store, error) {
	key, err := loadKey(keyFile)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, key: key}, nil
}

func loadKey(keyFile string) ([]byte, error) {
	if env := strings.TrimSpace(os.Getenv("GK_SECRET_KEY")); env != "" {
		if b, err := hex.DecodeString(env); err == nil && len(b) == 32 {
			return b, nil
		}
		sum := sha256.Sum256([]byte(env)) // 允许任意口令
		return sum[:], nil
	}
	if b, err := os.ReadFile(keyFile); err == nil {
		if k, err := hex.DecodeString(strings.TrimSpace(string(b))); err == nil && len(k) == 32 {
			return k, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return nil, err
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyFile, []byte(hex.EncodeToString(k)), 0o600); err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "已生成配置加密密钥: %s（请备份；丢失后需重新填写 API key）\n", keyFile)
	return k, nil
}

// Get 取配置值。不区分敏感与否——调用方自己清楚拿它去干什么。
// 面向界面或 API 的读取请用 GetMasked。
func (s *Store) Get(ctx context.Context, key string) (string, error) {
	var value sql.NullString
	var secret []byte
	var isSecret int
	err := s.db.QueryRowContext(ctx,
		`SELECT value, value_secret, is_secret FROM setting WHERE key = ?`, key).
		Scan(&value, &secret, &isSecret)
	if errors.Is(err, sql.ErrNoRows) {
		if d, ok := Defaults[key]; ok {
			return d, nil
		}
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if isSecret == 1 {
		if len(secret) == 0 {
			return "", nil
		}
		return s.decrypt(secret)
	}
	return value.String, nil
}

// GetMasked 取配置值用于展示：敏感且有值时返回掩码，绝不返回明文。
func (s *Store) GetMasked(ctx context.Context, key string) (value string, set bool, err error) {
	v, err := s.Get(ctx, key)
	if err != nil {
		return "", false, err
	}
	if isSecretKey(key) {
		if v == "" {
			return "", false, nil
		}
		return Mask, true, nil
	}
	return v, v != "", nil
}

// Set 写配置。isSecretKey 判定为敏感的键会加密后再存。
func (s *Store) Set(ctx context.Context, key, value, by string) error {
	now := time.Now().Format(time.RFC3339)
	if isSecretKey(key) {
		enc, err := s.encrypt(value)
		if err != nil {
			return err
		}
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO setting(key, value, value_secret, is_secret, updated_at, updated_by)
			 VALUES (?, NULL, ?, 1, ?, ?)
			 ON CONFLICT(key) DO UPDATE SET value_secret=excluded.value_secret,
			   is_secret=1, value=NULL, updated_at=excluded.updated_at, updated_by=excluded.updated_by`,
			key, enc, now, by)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO setting(key, value, value_secret, is_secret, updated_at, updated_by)
		 VALUES (?, ?, NULL, 0, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value,
		   is_secret=0, value_secret=NULL, updated_at=excluded.updated_at, updated_by=excluded.updated_by`,
		key, value, now, by)
	return err
}

// isSecretKey 判断某个键是否为敏感值。
func isSecretKey(key string) bool {
	return key == KeyAPIKey
}

func (s *Store) encrypt(plain string) ([]byte, error) {
	if plain == "" {
		return nil, nil
	}
	gcm, err := s.gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(plain), nil), nil
}

func (s *Store) decrypt(blob []byte) (string, error) {
	gcm, err := s.gcm()
	if err != nil {
		return "", err
	}
	if len(blob) < gcm.NonceSize() {
		return "", fmt.Errorf("密文长度异常")
	}
	nonce, ct := blob[:gcm.NonceSize()], blob[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("解密失败（密钥是否被更换过？）: %w", err)
	}
	return string(plain), nil
}

func (s *Store) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// All 返回所有配置（敏感值已遮蔽），供管理界面展示。
func (s *Store) All(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range Defaults {
		out[k] = v
	}
	rows, err := s.db.QueryContext(ctx, `SELECT key, value, value_secret, is_secret FROM setting`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var value sql.NullString
		var secret []byte
		var isSecret int
		if err := rows.Scan(&key, &value, &secret, &isSecret); err != nil {
			return nil, err
		}
		if isSecret == 1 {
			if len(secret) > 0 {
				out[key] = Mask
			} else {
				out[key] = ""
			}
			continue
		}
		out[key] = value.String
	}
	return out, rows.Err()
}
