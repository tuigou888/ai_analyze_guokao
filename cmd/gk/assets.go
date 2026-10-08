package main

import (
	"bufio"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"golang.org/x/term"
)

// webAssets 返回内嵌的前端构建产物。
//
// 前端在 web/ 下构建，产物 web/dist 由嵌入指令打进二进制，因此交付仍是一个
// 可执行文件（见 docs/架构方案.md §8.6）。受版本管理的 .gitkeep 使无前端
// 产物时仍可编译后端 CLI；缺少 index.html 时返回 nil，
// 由 serve 层给提示页——这样在没装 Node 的机器上后端仍可单独跑起来。
//
//go:embed all:web_dist
var webDist embed.FS

func webAssets() fs.FS {
	sub, err := fs.Sub(webDist, "web_dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// readPassword 交互式读取口令，不回显。
// 不把密码放在命令行参数里是刻意的：那会被写进 shell 历史和进程列表。
func readPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", err
		}
		s := strings.TrimSpace(string(b))
		if len(s) < 8 {
			return "", fmt.Errorf("密码至少 8 位")
		}
		return s, nil
	}
	// 非交互（管道/CI）：从 stdin 读一行
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return "", fmt.Errorf("读取密码失败: %w", sc.Err())
	}
	s := strings.TrimSpace(sc.Text())
	if len(s) < 8 {
		return "", fmt.Errorf("密码至少 8 位")
	}
	return s, nil
}
