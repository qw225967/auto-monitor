package lighter

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/analytics"
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/exchange"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/utils/logger"
	"github.com/qw225967/auto-monitor/internal/utils/rest"

	lighterClient "github.com/elliottech/lighter-go/client"
)

var _ exchange.Exchange = (*lighter)(nil)

type lighter struct {
	mu                       sync.RWMutex
	tickerCallback           exchange.TickerCallback
	subscribedSpotSymbols    map[string]bool
	subscribedFuturesSymbols map[string]bool
	isInitialized            bool
	apiKey                   string
	apiSecret                string
	token                    string      // Lighter 的认证 Token
	tokenExpiry              time.Time   // Token 过期时间
	accountIndex             int64       // 账户索引（默认 1，主账户）
	apiKeyIndex              uint8       // API Key 索引（默认 255，使用默认）

	restClient rest.RestClient
	txClient   *lighterClient.TxClient // Lighter SDK 交易客户端（用于签名，预留）
	wsClient   *WebSocketClient
}

// NewLighter 创建 Lighter DEX 实例
// apiKey: Lighter API Key (public key)
// apiSecret: Lighter API Secret (private key，用于签名)
func NewLighter(apiKey, apiSecret string) exchange.Exchange {
	// 从全局配置获取 accountIndex 和 apiKeyIndex
	globalConfig := config.GetGlobalConfig()
	accountIndex := globalConfig.Lighter.AccountIndex
	apiKeyIndex := globalConfig.Lighter.APIKeyIndex

	return &lighter{
		apiKey:                   apiKey,
		apiSecret:                apiSecret,
		subscribedSpotSymbols:    make(map[string]bool),
		subscribedFuturesSymbols: make(map[string]bool),
		isInitialized:            false,
		accountIndex:             accountIndex,
		apiKeyIndex:              apiKeyIndex,
	}
}

func (l *lighter) GetType() string {
	return constants.ConnectTypeLighter
}

func (l *lighter) Init() error {
	l.mu.Lock()
	if l.isInitialized {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()

	// 初始化 REST 客户端（用于通用 HTTP 请求，已配置代理）
	l.restClient.InitRestClient()

	// 获取认证 Token（refreshToken 内部会获取锁）
	// 注意：首次获取 token 时，account_index 和 api_key_index 可能还未设置
	// 此时会使用时间戳作为 nonce 的回退方案
	if err := l.refreshToken(); err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	// 验证 Token 是否已设置（需要读锁）
	l.mu.RLock()
	token := l.token
	l.mu.RUnlock()

	if token == "" {
		return fmt.Errorf("token is empty after refresh")
	}

	// 获取 token 后，查询账户信息以获取 account_index 和 api_key_index
	// 这些信息用于后续获取 nonce
	if err := l.queryAccountInfo(); err != nil {
		logger.GetLoggerInstance().Named("lighter").Sugar().Warnf(
			"Failed to query account info, nonce will use fallback: %v", err)
		// 继续，nonce 获取会使用回退方案
	}

	// 初始化 WebSocket 客户端（延迟连接，不在 Init 时连接）
	l.mu.Lock()
	l.wsClient = NewWebSocketClient(l.apiKey, token, l.tickerCallback)
	logger.GetLoggerInstance().Named("lighter").Sugar().Info("Lighter DEX initialized successfully")
	l.isInitialized = true
	l.mu.Unlock()

	return nil
}

// SubscribeTicker 订阅 ticker
// 将新 symbol 合并到已订阅集合，并用完整列表重新订阅 WS，保证多 symbol 同时订阅不互相覆盖
func (l *lighter) SubscribeTicker(spotSymbols, futuresSymbols []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.isInitialized {
		return exchange.ErrNotInitialized
	}

	// 1. 合并到已订阅集合
	for _, s := range spotSymbols {
		l.subscribedSpotSymbols[s] = true
	}
	for _, s := range futuresSymbols {
		l.subscribedFuturesSymbols[s] = true
	}

	// 2. 用完整列表订阅（Lighter 合并 spot+futures 为一个 list）
	allSymbols := make([]string, 0, len(l.subscribedSpotSymbols)+len(l.subscribedFuturesSymbols))
	for s := range l.subscribedSpotSymbols {
		allSymbols = append(allSymbols, s)
	}
	for s := range l.subscribedFuturesSymbols {
		allSymbols = append(allSymbols, s)
	}
	if err := l.wsClient.Subscribe(allSymbols); err != nil {
		return err
	}

	logger.GetLoggerInstance().Named("lighter").Sugar().Infof(
		"Subscribed to %d spot and %d futures symbols",
		len(l.subscribedSpotSymbols), len(l.subscribedFuturesSymbols))
	return nil
}

// UnsubscribeTicker 取消订阅
// 从已订阅集合移除，并用剩余列表重新订阅
func (l *lighter) UnsubscribeTicker(spotSymbols, futuresSymbols []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.isInitialized {
		return exchange.ErrNotInitialized
	}

	for _, s := range spotSymbols {
		delete(l.subscribedSpotSymbols, s)
	}
	for _, s := range futuresSymbols {
		delete(l.subscribedFuturesSymbols, s)
	}

	allSymbols := make([]string, 0, len(l.subscribedSpotSymbols)+len(l.subscribedFuturesSymbols))
	for s := range l.subscribedSpotSymbols {
		allSymbols = append(allSymbols, s)
	}
	for s := range l.subscribedFuturesSymbols {
		allSymbols = append(allSymbols, s)
	}
	if len(allSymbols) > 0 {
		if err := l.wsClient.Subscribe(allSymbols); err != nil {
			return err
		}
	}

	logger.GetLoggerInstance().Named("lighter").Sugar().Infof(
		"Unsubscribed %d spot and %d futures symbols",
		len(spotSymbols), len(futuresSymbols))
	return nil
}

// SetTickerCallback 设置 ticker 回调
func (l *lighter) SetTickerCallback(callback exchange.TickerCallback) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tickerCallback = callback
	if l.wsClient != nil {
		l.wsClient.SetCallback(callback)
	}
}

// PlaceOrder 下单
func (l *lighter) PlaceOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	l.mu.RLock()
	if !l.isInitialized {
		l.mu.RUnlock()
		return nil, exchange.ErrNotInitialized
	}
	l.mu.RUnlock()

	// 刷新 Token（如果需要）
	if err := l.ensureValidToken(); err != nil {
		return nil, err
	}

	return l.placeOrderInternal(req)
}

// GetBalance 获取余额
func (l *lighter) GetBalance() (*model.Balance, error) {
	l.mu.RLock()
	if !l.isInitialized {
		l.mu.RUnlock()
		return nil, exchange.ErrNotInitialized
	}
	l.mu.RUnlock()

	if err := l.ensureValidToken(); err != nil {
		return nil, err
	}

	return l.getAccountBalance()
}

// GetPosition 获取持仓
func (l *lighter) GetPosition(symbol string) (*model.Position, error) {
	l.mu.RLock()
	if !l.isInitialized {
		l.mu.RUnlock()
		return nil, exchange.ErrNotInitialized
	}
	l.mu.RUnlock()

	if err := l.ensureValidToken(); err != nil {
		return nil, err
	}

	return l.getPositionBySymbol(symbol)
}

// GetPositions 获取所有持仓
func (l *lighter) GetPositions() ([]*model.Position, error) {
	l.mu.RLock()
	if !l.isInitialized {
		l.mu.RUnlock()
		return nil, exchange.ErrNotInitialized
	}
	l.mu.RUnlock()

	if err := l.ensureValidToken(); err != nil {
		return nil, err
	}

	return l.getAllPositions()
}

// GetAllBalances 获取所有余额
func (l *lighter) GetAllBalances() (map[string]*model.Balance, error) {
	l.mu.RLock()
	if !l.isInitialized {
		l.mu.RUnlock()
		return nil, exchange.ErrNotInitialized
	}
	l.mu.RUnlock()

	if err := l.ensureValidToken(); err != nil {
		return nil, err
	}

	return l.getAllAccountBalances()
}

// GetSpotBalances 获取现货账户余额（Lighter 暂不支持分别获取，返回统一余额）
func (l *lighter) GetSpotBalances() (map[string]*model.Balance, error) {
	return l.GetAllBalances()
}

// GetFuturesBalances 获取合约账户余额（Lighter 暂不支持分别获取，返回统一余额）
func (l *lighter) GetFuturesBalances() (map[string]*model.Balance, error) {
	return l.GetAllBalances()
}

// GetSpotOrderBook 获取现货订单簿
func (l *lighter) GetSpotOrderBook(symbol string) (bids [][]string, asks [][]string, err error) {
	return l.getOrderBook(symbol, false)
}

// GetFuturesOrderBook 获取合约订单簿
func (l *lighter) GetFuturesOrderBook(symbol string) (bids [][]string, asks [][]string, err error) {
	return l.getOrderBook(symbol, true)
}

// CalculateSlippage 计算滑点
func (l *lighter) CalculateSlippage(symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64) {
	return analytics.CalculateExchangeSlippage(l, symbol, amount, isFutures, side, slippageLimit)
}

// ensureValidToken 确保 Token 有效，如果过期则刷新
func (l *lighter) ensureValidToken() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// 如果 Token 即将过期（提前 5 分钟刷新）
	if time.Now().Add(5 * time.Minute).After(l.tokenExpiry) {
		return l.refreshToken()
	}

	return nil
}

// getNextNonce 获取下一个 Nonce
// 优先从服务器获取（使用自己的 restClient，支持代理），如果失败则使用时间戳作为回退
func (l *lighter) getNextNonce() int64 {
	// 尝试从服务器获取 nonce（使用自己的 restClient，支持代理配置）
	nonce, err := l.fetchNonceFromServer()
	if err == nil {
		return nonce
	}
	// 如果获取失败，记录错误并使用回退方案
	logger.GetLoggerInstance().Named("lighter").Sugar().Warnf(
		"Failed to get nonce from server, using fallback: %v", err)

	// 回退方案：使用时间戳（毫秒）
	// 注意：这不是最佳实践，应该从服务器获取 nonce
	return time.Now().UnixNano() / int64(time.Millisecond)
}

// queryAccountInfo 查询账户信息以获取 account_index 和 api_key_index
// 注意：此方法需要在获取 auth token 后调用
func (l *lighter) queryAccountInfo() error {
	l.mu.RLock()
	token := l.token
	l.mu.RUnlock()

	if token == "" {
		return fmt.Errorf("token not available, cannot query account info")
	}

	// 1. 查询账户信息以获取 account_index
	accountIndex, err := l.queryAccountIndex()
	if err != nil {
		return fmt.Errorf("failed to query account index: %w", err)
	}

	// 2. 更新 account_index（先更新，因为查询 API keys 需要它）
	l.mu.Lock()
	l.accountIndex = accountIndex
	l.mu.Unlock()

	// 3. 查询 API keys 以获取 api_key_index（通过匹配当前的 apiKey）
	apiKeyIndex, err := l.queryAPIKeyIndex(accountIndex)
	if err != nil {
		// API key index 查询失败不影响整体流程，记录警告即可
		logger.GetLoggerInstance().Named("lighter").Sugar().Warnf(
			"Failed to query API key index: %v", err)
		// 使用默认值 255（表示使用默认 API key）
		apiKeyIndex = 255
	}

	// 4. 更新 api_key_index
	l.mu.Lock()
	l.apiKeyIndex = apiKeyIndex
	l.mu.Unlock()

	logger.GetLoggerInstance().Named("lighter").Sugar().Infof(
		"Account info updated: account_index=%d, api_key_index=%d", accountIndex, apiKeyIndex)

	return nil
}

// queryAccountIndex 查询账户索引
func (l *lighter) queryAccountIndex() (int64, error) {
	l.mu.RLock()
	token := l.token
	apiKey := l.apiKey
	l.mu.RUnlock()

	nonce := l.getNextNonce()
	headers := buildAuthHeaders(apiKey, token, nonce)

	// 查询账户信息（通过 L1 地址或 API key）
	// 根据文档，可以使用 /v1/account 端点
	apiURL := fmt.Sprintf("%s%s", constants.LighterRestBaseUrl, constants.LighterAccountPath)

	responseBody, err := l.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return 0, fmt.Errorf("failed to query account: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return 0, err
	}

	var resp struct {
		AccountIndex int64 `json:"account_index"`
		// 可能还有其他字段
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return 0, fmt.Errorf("failed to parse account response: %w", err)
	}

	if resp.AccountIndex <= 0 {
		return 0, fmt.Errorf("invalid account_index: %d", resp.AccountIndex)
	}

	return resp.AccountIndex, nil
}

// queryAPIKeyIndex 查询 API Key 索引（通过匹配当前的 apiKey）
func (l *lighter) queryAPIKeyIndex(accountIndex int64) (uint8, error) {
	l.mu.RLock()
	token := l.token
	apiKey := l.apiKey
	l.mu.RUnlock()

	if accountIndex <= 0 {
		return 0, fmt.Errorf("invalid account_index: %d", accountIndex)
	}

	nonce := l.getNextNonce()
	headers := buildAuthHeaders(apiKey, token, nonce)

	// 查询所有 API keys（使用 api_key_index = 255 获取所有 keys）
	// 根据文档，可以使用 /v1/account/apikeys 端点
	apiURL := fmt.Sprintf("%s%s?account_index=%d&api_key_index=255",
		constants.LighterRestBaseUrl, constants.LighterAccountAPIKeysPath, accountIndex)

	responseBody, err := l.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return 0, fmt.Errorf("failed to query API keys: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return 0, err
	}

	var resp struct {
		ApiKeys []struct {
			ApiKeyIndex uint8  `json:"api_key_index"`
			PublicKey   string `json:"public_key"`
		} `json:"api_keys"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return 0, fmt.Errorf("failed to parse API keys response: %w", err)
	}

	// 匹配当前的 apiKey
	for _, key := range resp.ApiKeys {
		if key.PublicKey == apiKey {
			return key.ApiKeyIndex, nil
		}
	}

	return 0, fmt.Errorf("API key not found in account keys")
}

// fetchNonceFromServer 从服务器获取下一个 nonce（使用 restClient，支持代理）
// 注意：此方法需要正确的 account_index 和 api_key_index
func (l *lighter) fetchNonceFromServer() (int64, error) {
	apiURL := fmt.Sprintf("%s/api/v1/nextNonce?account_index=%d&api_key_index=%d",
		constants.LighterRestBaseUrl, l.accountIndex, l.apiKeyIndex)

	headers := make(map[string]string)
	headers["Content-Type"] = "application/json"

	responseBody, err := l.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch nonce: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return 0, err
	}

	var resp struct {
		Code  string `json:"code"`
		Nonce int64  `json:"nonce"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return 0, fmt.Errorf("failed to parse nonce response: %w", err)
	}

	if resp.Code != "0" && resp.Code != "" {
		return 0, fmt.Errorf("API error: code=%s", resp.Code)
	}

	return resp.Nonce, nil
}
