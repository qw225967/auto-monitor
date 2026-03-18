package seeingstone

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/qw225967/auto-monitor/internal/model"
)

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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("[SeeingStone] fetch failed: %v", err)
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var apiResp model.SpreadAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("api returned success=false")
	}

	return apiResp.Data, nil
}
