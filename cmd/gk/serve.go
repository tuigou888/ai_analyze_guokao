package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ai_analyze_guokao/internal/admin"
	"ai_analyze_guokao/internal/serve"
	"ai_analyze_guokao/internal/setting"
	"ai_analyze_guokao/internal/store"
	"ai_analyze_guokao/internal/study"
)

const secretKeyFile = "var/secret.key"

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	dbFile := fs.String("db", "var/db/gk.sqlite", "数据库路径")
	dataDir := fs.String("data", "data", "图片根目录（包含题目图/公式图）")
	addr := fs.String("addr", "127.0.0.1:8080", "监听地址")
	keyFile := fs.String("secret-key-file", secretKeyFile, "配置加密密钥文件")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := store.Open(*dbFile)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	n, err := admin.Count(ctx, db)
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("尚未创建管理员账号，请先执行：gk admin create --user <用户名>")
	}

	settings, err := setting.Open(db, *keyFile)
	if err != nil {
		return err
	}

	if err := (&study.Service{DB: db}).RefreshConcepts(ctx); err != nil {
		return err
	}
	srv := serve.New(settings, db, webAssets())
	srv.DataDir = *dataDir
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}

	go func() {
		fmt.Printf("gk 服务已启动: http://%s\n", *addr)
		fmt.Println("管理入口: /admin（接口 /api/admin/*）")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "服务异常退出:", err)
			os.Exit(1)
		}
	}()

	// 优雅退出：等正在处理的请求结束，而不是直接杀进程。
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	fmt.Println("\n正在关闭…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

func cmdAdmin(args []string) error {
	if len(args) == 0 {
		fmt.Print(`gk admin —— 管理入口账号

用法:
  gk admin create --user <用户名> [--password <密码>]   创建管理员
  gk admin passwd --user <用户名> [--password <密码>]   修改口令（会吊销所有旧会话）

选项:
  --db string   数据库路径 (默认 "var/db/gk.sqlite")
  --password    不传则交互式输入，避免口令留在 shell 历史里
`)
		return nil
	}
	sub := args[0]
	fs := flag.NewFlagSet("admin "+sub, flag.ContinueOnError)
	dbFile := fs.String("db", "var/db/gk.sqlite", "数据库路径")
	user := fs.String("user", "admin", "用户名")
	password := fs.String("password", "", "密码（不传则交互输入）")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	db, err := store.Open(*dbFile)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()

	pw := *password
	if pw == "" {
		pw, err = readPassword(fmt.Sprintf("请输入 %s 的密码（至少 8 位）: ", *user))
		if err != nil {
			return err
		}
	}

	switch sub {
	case "create":
		if err := admin.Create(ctx, db, *user, pw); err != nil {
			return err
		}
		fmt.Printf("已创建管理员 %s\n下一步: gk serve，然后访问 /admin 填写 API 地址与密钥\n", *user)
	case "passwd":
		if err := admin.SetPassword(ctx, db, *user, pw); err != nil {
			return err
		}
		fmt.Printf("已更新 %s 的密码，所有旧会话已吊销\n", *user)
	default:
		return fmt.Errorf("未知 admin 子命令 %q", sub)
	}
	return nil
}

func cmdConfig(args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	// 手动扫描选项与位置参数：Go 的 flag 包遇到第一个非 flag 参数就停止解析，
	// 而 `gk config set llm.model X --db Y` 这种写法很自然，用 flag 会直接报错。
	dbFile := "var/db/gk.sqlite"
	keyFile := secretKeyFile
	var positional []string
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--db":
			if i+1 < len(rest) {
				dbFile = rest[i+1]
				i++
			}
		case "--secret-key-file":
			if i+1 < len(rest) {
				keyFile = rest[i+1]
				i++
			}
		default:
			positional = append(positional, rest[i])
		}
	}

	db, err := store.Open(dbFile)
	if err != nil {
		return err
	}
	defer db.Close()
	st, err := setting.Open(db, keyFile)
	if err != nil {
		return err
	}
	ctx := context.Background()

	switch args[0] {
	case "list", "get":
		all, err := st.All(ctx)
		if err != nil {
			return err
		}
		fmt.Println("配置（敏感值已遮蔽；批量修改请用管理界面）:")
		for _, k := range []string{
			setting.KeyBaseURL, setting.KeyModel, setting.KeyAPIKey,
			setting.KeyConcurrency, setting.KeyTimeoutSec, setting.KeyMaxTokens, setting.KeyMaxRetries,
			setting.KeyDailyBudget, setting.KeyModelPricing,
		} {
			v := all[k]
			if v == "" {
				v = "(未设置)"
			}
			fmt.Printf("  %-24s %s\n", k, v)
		}
		fmt.Printf("\n密钥文件: %s（可用环境变量 GK_SECRET_KEY 覆盖）\n", keyFile)
		fmt.Println("可用模型见: gk llm models")
	case "set":
		if len(positional) < 2 {
			return errors.New("用法: gk config set <key> <value> [--db 路径]")
		}
		if err := st.Set(ctx, positional[0], positional[1], "cli"); err != nil {
			return err
		}
		fmt.Printf("已设置 %s = %s\n", positional[0], positional[1])
	default:
		return fmt.Errorf("未知 config 子命令 %q（支持 list / set）", args[0])
	}
	return nil
}
