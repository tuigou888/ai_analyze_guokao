package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ai_analyze_guokao/internal/llm"
	"ai_analyze_guokao/internal/setting"
	"ai_analyze_guokao/internal/store"
)

func cmdLLM(args []string) error {
	if len(args) == 0 {
		fmt.Print(`gk llm —— 接口自检

用法:
  gk llm models [--all]   列出该 key 可用的模型（--all 一并列出网关的全部模型）
  gk llm ping [--model X] 用当前配置打一次最小请求，核对回执模型名
                          --model 可临时换模型探测，不改配置

为什么需要它：配置里写的模型名只能证明我们请求了什么，证明不了它存在、
也没证明请求路由到了哪里。查网关的模型目录是比"回执回显"更硬的证据
（docs/架构方案.md §6.4）。
`)
		return nil
	}
	fs := flag.NewFlagSet("llm", flag.ContinueOnError)
	dbFile := fs.String("db", "var/db/gk.sqlite", "数据库路径")
	keyFile := fs.String("secret-key-file", secretKeyFile, "配置加密密钥文件")
	all := fs.Bool("all", false, "列出全部模型（含不可用的）")
	modelOverride := fs.String("model", "", "临时用指定模型探测（不修改配置）")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	db, err := store.Open(*dbFile)
	if err != nil {
		return err
	}
	defer db.Close()
	st, err := setting.Open(db, *keyFile)
	if err != nil {
		return err
	}
	ctx := context.Background()
	baseURL, _ := st.Get(ctx, setting.KeyBaseURL)
	apiKey, _ := st.Get(ctx, setting.KeyAPIKey)
	model, _ := st.Get(ctx, setting.KeyModel)

	if baseURL == "" || apiKey == "" {
		return errors.New("请先在管理入口或 gk config set 里配置 llm.base_url 与 llm.api_key")
	}

	switch args[0] {
	case "models":
		return llmModels(baseURL, apiKey, model, *all)
	case "ping":
		probe := model
		if *modelOverride != "" {
			probe = *modelOverride
		}
		client := llm.New(baseURL, apiKey, 120*time.Second)
		respModel, latency, err := client.Ping(ctx, probe)
		if err != nil {
			return err
		}
		fmt.Printf("请求成功\n  耗时      %d ms\n  请求模型  %s\n  回执模型  %s\n",
			latency.Milliseconds(), probe, orDash(respModel))
		if respModel != "" && respModel != probe {
			fmt.Println("⚠ 两者不一致：请确认网关路由与计费口径")
		}
		return nil
	default:
		return fmt.Errorf("未知 llm 子命令 %q", args[0])
	}
}

// llmModels 拉取网关的模型目录。这是判断"模型名是否有效"最直接的证据，
// 比从一次失败调用的错误信息反推可靠得多。
func llmModels(baseURL, apiKey, configured string, all bool) error {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("请求 /models 失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /models 返回 HTTP %d: %s", resp.StatusCode, snippetOf(body))
	}

	// OpenAI 兼容格式通常是 {"data":[{"id":"..."}]}，但网关各异，
	// 这里做一次宽松解析：优先取 data，退化到顶层数组。
	var parsed struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || len(parsed.Data) == 0 {
		fmt.Println("网关返回的不是标准 /models 结构，原始内容前 500 字：")
		fmt.Println(snippetOf(body))
		return nil
	}

	fmt.Printf("该 key 可见 %d 个模型：\n", len(parsed.Data))
	found := false
	matches := 0
	for _, m := range parsed.Data {
		if !all && configured != "" && !strings.Contains(m.ID, configured) &&
			!strings.Contains(configured, m.ID) {
			continue
		}
		mark := "  "
		if m.ID == configured {
			mark = "✅"
			found = true
		}
		fmt.Printf("  %s %s\n", mark, m.ID)
		matches++
	}
	if !all {
		fmt.Printf("\n（只显示与配置 %q 相关的条目；加 --all 看全部）\n", configured)
	}
	if configured != "" && !found {
		fmt.Printf("\n⚠ 配置的模型 %q 不在该 key 的可用列表里。\n"+
			"  常见原因是该 key 未开通此模型——此时网关可能返回 404、也可能是 429，\n"+
			"  从错误信息反推不可靠，以上列表才是依据。\n", configured)
	}
	return nil
}

func snippetOf(b []byte) string {
	s := strings.TrimSpace(string(b))
	r := []rune(s)
	if len(r) > 500 {
		return string(r[:500]) + "…"
	}
	return s
}
