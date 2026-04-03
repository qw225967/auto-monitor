package seeingstone

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/qw225967/auto-monitor/internal/model"
)

const maxErrBodyLog = 512

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

func describeBearer(token string) string {
	if token == "" {
		return "none"
	}
	return fmt.Sprintf("Bearer(len=%d)", len(token))
}

const (
	DataName    = "seeingstone"
	DataType    = "spread"
	spreadsPath = "/api/spreads"
)

// Config SeeingStone 配置
type Config struct {
	BaseURL        string
	Token          string
	RequestTimeout time.Duration
}

// Adapter SeeingStone 价差数据源
type Adapter struct {
	client  *http.Client
	baseURL string
	token   string
}

// New 创建 SeeingStone 适配器
func New(cfg Config) *Adapter {
	timeout := cfg.RequestTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	log.Printf("[SeeingStone] 适配器初始化 baseURL=%s timeout=%v %s",
		strings.TrimRight(cfg.BaseURL, "/"), timeout, describeBearer(cfg.Token))
	return &Adapter{
		client:  &http.Client{Timeout: timeout},
		baseURL: cfg.BaseURL,
		token:   cfg.Token,
	}
}

// Name 实现 DataSource
func (a *Adapter) Name() string {
	return DataName
}

// DataType 实现 DataSource
func (a *Adapter) DataType() string {
	return DataType
}

// Fetch 实现 DataSource，拉取价差数据
func (a *Adapter) Fetch(ctx context.Context) (interface{}, error) {
	url := a.baseURL + spreadsPath
	log.Printf("[Fetch] GET %s auth=%s", url, describeBearer(a.token))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.Printf("[Fetch] 构造请求失败: %v", err)
		return nil, fmt.Errorf("create request: %w", err)
	}
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("[Fetch] 请求失败: %v", err)
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		log.Printf("[Fetch] 非 200: status=%d url=%s body=%s",
			resp.StatusCode, url, truncateForLog(bodyStr, maxErrBodyLog))
		if resp.StatusCode == http.StatusUnauthorized {
			if a.token == "" {
				log.Printf("[Fetch] 401 且无 Bearer：请设置 SEEINGSTONE_API_TOKEN，并确认 [Config] 中已显示 api_token=已设置")
			} else {
				log.Printf("[Fetch] 401 且已带 Bearer：请检查 Token 是否过期或 API 权限")
			}
		}
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, bodyStr)
	}

	var apiResp model.SpreadAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		log.Printf("[Fetch] JSON 解码失败: %v", err)
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if !apiResp.Success {
		log.Printf("[Fetch] API 返回 success=false（无 data 或业务错误）")
		return nil, fmt.Errorf("api returned success=false")
	}

	log.Printf("[Fetch] 成功 spreads=%d", len(apiResp.Data))

	// SeeingStone /api/spreads 仅返回 symbol, buy_exchange, sell_exchange, spread_percent 等，
	// 不包含 buy_price/sell_price/aBid/aAsk 等价格字段，价格需从 K 线等其它数据源获取

	return apiResp.Data, nil
}
