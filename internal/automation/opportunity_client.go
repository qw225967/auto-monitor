package automation

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"go.uber.org/zap"

	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
)

// OpportunityClient 套利机会客户端
type OpportunityClient struct {
	httpClient *http.Client
	logger     *zap.SugaredLogger
}

// NewOpportunityClient 创建新的套利机会客户端
func NewOpportunityClient() *OpportunityClient {
	return &OpportunityClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger.GetLoggerInstance().Named("OpportunityClient").Sugar(),
	}
}

// FetchOpportunities 拉取所有启用的API端点
// 新的API设计：所有端点使用同一个URL（getall），返回按类型分类的map
func (c *OpportunityClient) FetchOpportunities(endpoints []model.APIEndpointConfig) ([]model.ArbitrageOpportunity, error) {
	// 找到第一个启用的端点，使用它的URL（所有端点应该使用同一个getall URL）
	var apiURL string
	enabledEndpoints := make(map[string]bool) // 记录哪些端点类型是启用的
	for _, endpoint := range endpoints {
		if endpoint.Enabled {
			if apiURL == "" {
				apiURL = endpoint.URL
			}
			enabledEndpoints[endpoint.Type] = true
		}
	}

	if apiURL == "" {
		return nil, fmt.Errorf("no enabled endpoints found")
	}

	// 调用统一的getall接口
	allOpportunitiesMap, err := c.fetchAllOpportunities(apiURL, endpoints)
	if err != nil {
		return nil, err
	}

	// 从map中提取所有启用的端点类型的机会
	var allOpportunities []model.ArbitrageOpportunity
	for endpointType, opportunities := range allOpportunitiesMap {
		if enabledEndpoints[endpointType] {
			allOpportunities = append(allOpportunities, opportunities...)
		}
	}

	// 按利润统一排序（从高到低）
	sort.Slice(allOpportunities, func(i, j int) bool {
		return allOpportunities[i].Profit > allOpportunities[j].Profit
	})

	return allOpportunities, nil
}

// fetchAllOpportunities 拉取所有机会列表（按类型分类的map）
func (c *OpportunityClient) fetchAllOpportunities(apiURL string, endpoints []model.APIEndpointConfig) (map[string][]model.ArbitrageOpportunity, error) {
	// 使用第一个端点的API Key（如果所有端点使用同一个API，应该使用同一个Key）
	var apiKey string
	for _, endpoint := range endpoints {
		if endpoint.Enabled && endpoint.APIKey != "" {
			apiKey = endpoint.APIKey
			break
		}
	}

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// 添加API Key（如果需要）
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("X-API-Key", apiKey)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return c.ParseAllResponse(body)
}

// ParseAllResponse 解析getall接口的响应（按类型分类的map）
func (c *OpportunityClient) ParseAllResponse(body []byte) (map[string][]model.ArbitrageOpportunity, error) {
	var allOpportunities map[string][]model.ArbitrageOpportunity

	if err := json.Unmarshal(body, &allOpportunities); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	return allOpportunities, nil
}
