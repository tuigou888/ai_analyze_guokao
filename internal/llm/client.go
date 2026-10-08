// Package llm 是 OpenAI 兼容协议的客户端。
//
// 刻意手写而不引 SDK：网关会换、模型名会变、同一家网关可能有多套模型目录
// （参考文档 §3.7），一个约 200 行的 HTTP 客户端比任何 SDK 都更好控。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Message 一条对话消息。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request 一次对话请求。
type Request struct {
	Model       string
	Messages    []Message
	Temperature float64
	MaxTokens   int
	// JSONMode 请求 response_format={"type":"json_object"}。并非所有网关都支持，
	// 不支持时会退化成普通请求，因此调用方仍必须做 schema 校验。
	JSONMode bool
}

// Response 一次对话响应。
type Response struct {
	Content string
	// Model 是**回执**里的模型名。它与配置里写的名字不一致时必须告警：
	// 配置名只能证明我们请求了什么，证明不了实际路由到了什么（§3.7）。
	Model      string
	TokensIn   int
	TokensOut  int
	UsageKnown bool // Both counters were explicitly present and non-negative.
	Latency    time.Duration
}

// Error 是带分类的调用错误。分类决定重试策略（§3.5：可重试与不可重试必须分开）。
type Error struct {
	StatusCode int
	Retryable  bool
	Message    string
	// RetryAfter 来自 Retry-After 响应头（秒）。限流时网关常会给出建议等待时间，
	// 按它退避比按固定指数退避更不容易把限流撞得更死。
	RetryAfter time.Duration
	// RateLimited 表示这仍是**配额类**错误（429）。它需要按"窗口"退避：
	// rpm/tpm 是按分钟计的，退避几秒撞不上下一个窗口，重试基本白费。
	// 但 429 不消耗 token，所以多等、多试是零成本换成功率。
	RateLimited bool
	// Unavailable 表示上游**没有可用渠道**（网关侧的故障，典型报文是
	// "No available channel for model X under group Y"）。
	//
	// 它必须与限流区分开：限流是"你请求太快"，退避几秒就能继续；渠道故障是
	// "上游挂了"，秒级退避只会把重试次数在几十秒内烧光，然后整个批次以失败告终
	// ——而我们真正该做的是等它恢复。
	Unavailable bool
}

func (e *Error) Error() string {
	kind := "不可重试"
	if e.Retryable {
		kind = "可重试"
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("LLM 调用失败（HTTP %d, %s）: %s", e.StatusCode, kind, e.Message)
	}
	return fmt.Sprintf("LLM 调用失败（%s）: %s", kind, e.Message)
}

// Client 是 OpenAI 兼容客户端。
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// New 构造客户端。timeout 为单次请求超时。
func New(baseURL, apiKey string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: timeout},
	}
}

// Chat 发起一次对话补全。
func (c *Client) Chat(ctx context.Context, req Request) (*Response, error) {
	body := map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   false,
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.JSONMode {
		body["response_format"] = map[string]string{"type": "json_object"}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, &Error{Retryable: false, Message: "请求体序列化失败: " + err.Error()}
	}

	start := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, &Error{Retryable: false, Message: err.Error()}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		// 网络层失败（连接被拒、超时、TLS）一律可重试：多数是瞬时问题。
		return nil, &Error{Retryable: true, Message: err.Error()}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	latency := time.Since(start)
	if err != nil {
		return nil, &Error{StatusCode: resp.StatusCode, Retryable: true, Message: "读取响应失败: " + err.Error()}
	}
	var statusErr *Error
	if resp.StatusCode != http.StatusOK {
		e := classifyStatus(resp.StatusCode, raw)
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil && secs > 0 {
				e.RetryAfter = time.Duration(secs) * time.Second
			}
		}
		statusErr = e
	}

	var parsed struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     *int `json:"prompt_tokens"`
			CompletionTokens *int `json:"completion_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		if statusErr != nil {
			return nil, statusErr
		}
		return nil, &Error{StatusCode: resp.StatusCode, Retryable: true,
			Message: "响应不是合法 JSON（网关返回了 HTML？）: " + snippet(raw)}
	}
	result := &Response{Model: parsed.Model, Latency: latency}
	if parsed.Usage != nil {
		if p := parsed.Usage.PromptTokens; p != nil {
			result.TokensIn = max(*p, 0)
		}
		if p := parsed.Usage.CompletionTokens; p != nil {
			result.TokensOut = max(*p, 0)
		}
		result.UsageKnown = parsed.Usage.PromptTokens != nil && parsed.Usage.CompletionTokens != nil && *parsed.Usage.PromptTokens >= 0 && *parsed.Usage.CompletionTokens >= 0
	}
	if statusErr != nil {
		return result, statusErr
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return result, &Error{StatusCode: resp.StatusCode, Retryable: false, Message: parsed.Error.Message}
	}
	if len(parsed.Choices) == 0 {
		// 有时是内容被安全策略拦掉，重试一次通常仍失败，但不算致命。
		return result, &Error{StatusCode: resp.StatusCode, Retryable: true, Message: "响应中没有 choices"}
	}

	result.Content = parsed.Choices[0].Message.Content
	return result, nil
}

// classifyStatus 把 HTTP 状态码分成可重试与不可重试。
func classifyStatus(status int, body []byte) *Error {
	msg := extractErrorMessage(body)
	switch {
	case status == http.StatusTooManyRequests:
		return &Error{StatusCode: status, Retryable: true, RateLimited: true, Message: "限流: " + msg}
	case status == http.StatusBadGateway, status == http.StatusServiceUnavailable,
		status == http.StatusGatewayTimeout:
		e := &Error{StatusCode: status, Retryable: true, Message: msg}
		if strings.Contains(strings.ToLower(msg), "no available channel") ||
			strings.Contains(msg, "无可用渠道") {
			e.Unavailable = true
		}
		return e
	case status == http.StatusRequestTimeout:
		return &Error{StatusCode: status, Retryable: true, Message: msg}
	case status >= 500:
		return &Error{StatusCode: status, Retryable: true, Message: "服务端错误: " + msg}
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		// 认证问题是配置错误，重试只会白花时间，必须让人去改配置。
		return &Error{StatusCode: status, Retryable: false, Message: "鉴权失败（检查 API key）: " + msg}
	case status == http.StatusNotFound:
		return &Error{StatusCode: status, Retryable: false, Message: "端点或模型不存在（检查 base_url 与模型名）: " + msg}
	default:
		return &Error{StatusCode: status, Retryable: false, Message: msg}
	}
}

func extractErrorMessage(body []byte) string {
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil {
		if parsed.Error.Message != "" {
			return parsed.Error.Message
		}
		if parsed.Message != "" {
			return parsed.Message
		}
	}
	return snippet(body)
}

func snippet(b []byte) string {
	const max = 200
	s := strings.TrimSpace(string(b))
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// Ping 做一次最小可用性检查，供管理入口的"测试连接"用。
// 返回回执里的模型名与耗时——这也是核验"配置名是否等于实际模型"的最短路径。
func (c *Client) Ping(ctx context.Context, model string) (respModel string, latency time.Duration, err error) {
	resp, err := c.Chat(ctx, Request{
		Model:       model,
		Messages:    []Message{{Role: "user", Content: "ping"}},
		MaxTokens:   8,
		Temperature: 0,
	})
	if err != nil {
		return "", 0, err
	}
	return resp.Model, resp.Latency, nil
}

// ListModels 拉取网关的模型目录。
//
// 这是判断"某个模型名是否可用"最直接的证据：比从一次失败调用的错误信息反推可靠得多
// （§6.4 的模型自证原则，证据强度：模型目录 > 回执回显）。
func ListModels(ctx context.Context, baseURL, apiKey string, timeout time.Duration) ([]string, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /models 返回 HTTP %d: %s", resp.StatusCode, snippet(body))
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("解析 /models 响应失败: %w", err)
	}
	out := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Pricing 是价格表（美元 / 千 token）。
type Pricing struct {
	InputPer1K  float64
	OutputPer1K float64
}

// ParsePricing 解析 "输入价,输出价" 形式的配置，单位为美元/千 token。
func ParsePricing(s string) (Pricing, bool) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return Pricing{}, false
	}
	in, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	out, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil {
		return Pricing{}, false
	}
	return Pricing{InputPer1K: in, OutputPer1K: out}, true
}

// Cost 按 token 数估算花费。价格表缺失时返回 0，由调用方标注"成本未知"而不是谎报为 0 成本。
func (p Pricing) Cost(tokensIn, tokensOut int) float64 {
	return float64(tokensIn)/1000*p.InputPer1K + float64(tokensOut)/1000*p.OutputPer1K
}
