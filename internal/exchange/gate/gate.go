package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/analytics"
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/utils/rest"

	gate "github.com/gateio/gatews/go"
)

var _ exchange.Exchange                = (*gateExchange)(nil)
var _ exchange.QuantoMultiplierProvider = (*gateExchange)(nil)

const (
	spotFlag    = "spot"
	futuresFlag = "futures"
)

type gateExchange struct {
	mu sync.RWMutex

	// 回调函数
	tickerCallback exchange.TickerCallback

	// WebSocket 服务（分现货和合约）
	wsSpotService    *gate.WsService
	wsFuturesService *gate.WsService

	// HTTP REST 客户端
	restClient rest.RestClient

	// 订阅管理
	subscribedSpotSymbols    map[string]bool
	subscribedFuturesSymbols map[string]bool
	isInitialized            bool
	
	// Symbol 映射：Gate 格式 -> 原始格式 (例如: "RIVER_USDT" -> "RIVERUSDT")
	gateSymbolToOriginal map[string]string

	// 合约张数乘数：Gate 合约 name -> quanto_multiplier，用于 币数量/quanto_multiplier = 合约张数
	quantoMultipliers map[string]float64

	// 上下文管理
	ctx        context.Context
	cancelFunc context.CancelFunc

	// 重连相关
	reconnectMutex sync.Mutex
	isReconnecting bool
}

// NewGate 创建 Gate.io 交易所实例（API 密钥从全局配置获取）
func NewGate() exchange.Exchange {
	ctx, cancel := context.WithCancel(context.Background())
	return &gateExchange{
		subscribedSpotSymbols:    make(map[string]bool),
		subscribedFuturesSymbols: make(map[string]bool),
		isInitialized:            false,
		gateSymbolToOriginal:     make(map[string]string),
		quantoMultipliers:        make(map[string]float64),
		ctx:                      ctx,
		cancelFunc:               cancel,
	}
}

// getAPIKeys 获取 API 密钥（总是从全局配置读取最新值）
func (g *gateExchange) getAPIKeys() (string, string) {
	globalConfig := config.GetGlobalConfig()
	if globalConfig != nil {
		return globalConfig.Gate.APIKey, globalConfig.Gate.Secret
	}
	return "", ""
}

// GetType 获取交易所类型
func (g *gateExchange) GetType() string {
	return constants.ConnectTypeGate
}

// Init 初始化
func (g *gateExchange) Init() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.isInitialized {
		return nil
	}

	logInstance := logger.GetLoggerInstance().Named("gate").Sugar()

	// 获取 API 密钥（从全局配置读取）
	apiKey, secretKey := g.getAPIKeys()

	// 初始化现货 WebSocket（SDK 自动重连）
	var err error
	g.wsSpotService, err = gate.NewWsService(g.ctx, log.Default(),
		gate.NewConnConfFromOption(&gate.ConfOptions{
			URL:           constants.GateWsSpotUrl,
			Key:           apiKey,
			Secret:        secretKey,
			MaxRetryConn:  10, // SDK 自动重连
			SkipTlsVerify: false,
		}))
	if err != nil {
		return err
	}

	// 初始化合约 WebSocket（SDK 自动重连）
	g.wsFuturesService, err = gate.NewWsService(g.ctx, log.Default(),
		gate.NewConnConfFromOption(&gate.ConfOptions{
			URL:           constants.GateWsFuturesUrl,
			Key:           apiKey,
			Secret:        secretKey,
			MaxRetryConn:  10, // SDK 自动重连
			SkipTlsVerify: false,
		}))
	if err != nil {
		return err
	}

	// 设置回调
	g.setupCallbacks()

	// 初始化 REST 客户端
	g.restClient.InitRestClient()

	// 拉取合约列表并缓存 quanto_multiplier，用于下单时 币数量 -> 合约张数 换算
	if err := g.fetchAndCacheQuantoMultipliers(); err != nil {
		logInstance.Warnf("fetch quanto_multipliers failed (will use 1.0 as fallback): %v", err)
		// 不阻断初始化，下单时缺省按 1.0 换算
	}

	logInstance.Info("Gate.io exchange initialized successfully")
	g.isInitialized = true
	return nil
}

// fetchAndCacheQuantoMultipliers 调用 GET /futures/usdt/contracts 并缓存 quanto_multiplier
// 由 Init 在已持 g.mu 时调用，不再加锁
func (g *gateExchange) fetchAndCacheQuantoMultipliers() error {
	apiKey, secretKey := g.getAPIKeys()
	timestamp := getCurrentTimestamp()
	signature := signRequest("GET", constants.GateFuturesContractsPath, "", "", secretKey, timestamp)
	apiURL := constants.GateRestBaseUrl + constants.GateFuturesContractsPath
	headers := buildHeaders(apiKey, signature, timestamp)

	body, err := g.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return err
	}
	if err := checkAPIError(body); err != nil {
		return err
	}

	var list []struct {
		Name             string `json:"name"`
		QuantoMultiplier string `json:"quanto_multiplier"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		return err
	}

	for _, c := range list {
		mult := 1.0
		if c.QuantoMultiplier != "" {
			if v, err := strconv.ParseFloat(c.QuantoMultiplier, 64); err == nil && v > 0 {
				mult = v
			}
		}
		g.quantoMultipliers[c.Name] = mult
	}
	return nil
}

// setupCallbacks 设置回调函数
func (g *gateExchange) setupCallbacks() {
	// 现货 BookTicker 回调
	g.wsSpotService.SetCallBack(gate.ChannelSpotBookTicker, g.handleSpotTickerUpdate)

	// 合约 BookTicker 回调
	g.wsFuturesService.SetCallBack(gate.ChannelFutureBookTicker, g.handleFuturesTickerUpdate)
}

// isConnectionError 检测是否为连接错误（需要重连）
func (g *gateExchange) isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "use of closed network connection")
}

// reconnectWebSocket 重新初始化 WebSocket 连接
// 注意：调用此方法时不应持有 g.mu 锁，方法内部会使用 g.mu 保护 wsService 的访问
func (g *gateExchange) reconnectWebSocket(serviceType string) error {
	logInstance := logger.GetLoggerInstance().Named("gate.reconnect").Sugar()
	
	// 检查是否正在重连，避免重复重连
	g.reconnectMutex.Lock()
	if g.isReconnecting {
		g.reconnectMutex.Unlock()
		logInstance.Debugf("%s 正在重连中，等待重连完成...", serviceType)
		// 等待重连完成
		for i := 0; i < 10; i++ {
			time.Sleep(500 * time.Millisecond)
			g.reconnectMutex.Lock()
			if !g.isReconnecting {
				g.reconnectMutex.Unlock()
				logInstance.Debugf("%s 重连已完成", serviceType)
				return nil
			}
			g.reconnectMutex.Unlock()
		}
		return nil
	}
	g.isReconnecting = true
	g.reconnectMutex.Unlock()

	defer func() {
		g.reconnectMutex.Lock()
		g.isReconnecting = false
		g.reconnectMutex.Unlock()
	}()

	logInstance.Warnf("开始重连 %s WebSocket...", serviceType)

	// 获取 API 密钥（不需要锁）
	apiKey, secretKey := g.getAPIKeys()

	// 重新初始化对应的 WebSocket 服务（需要锁保护）
	g.mu.Lock()
	var err error
	if serviceType == "spot" {
		g.wsSpotService, err = gate.NewWsService(g.ctx, log.Default(),
			gate.NewConnConfFromOption(&gate.ConfOptions{
				URL:           constants.GateWsSpotUrl,
				Key:           apiKey,
				Secret:        secretKey,
				MaxRetryConn:  10,
				SkipTlsVerify: false,
			}))
		if err != nil {
			g.mu.Unlock()
			logInstance.Errorf("重连现货 WebSocket 失败: %v", err)
			return err
		}
		// 重新设置回调
		g.wsSpotService.SetCallBack(gate.ChannelSpotBookTicker, g.handleSpotTickerUpdate)
	} else if serviceType == "futures" {
		g.wsFuturesService, err = gate.NewWsService(g.ctx, log.Default(),
			gate.NewConnConfFromOption(&gate.ConfOptions{
				URL:           constants.GateWsFuturesUrl,
				Key:           apiKey,
				Secret:        secretKey,
				MaxRetryConn:  10,
				SkipTlsVerify: false,
			}))
		if err != nil {
			g.mu.Unlock()
			logInstance.Errorf("重连合约 WebSocket 失败: %v", err)
			return err
		}
		// 重新设置回调
		g.wsFuturesService.SetCallBack(gate.ChannelFutureBookTicker, g.handleFuturesTickerUpdate)
	}
	g.mu.Unlock()

	logInstance.Infof("%s WebSocket 重连成功", serviceType)
	return nil
}

// SubscribeTicker 订阅 ticker 价格数据
// 将新 symbol 合并到已订阅集合，并用完整列表重新订阅 WS，保证多 symbol 同时订阅不互相覆盖
func (g *gateExchange) SubscribeTicker(spotSymbols, futuresSymbols []string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.isInitialized {
		return exchange.ErrNotInitialized
	}

	logInstance := logger.GetLoggerInstance().Named("gate").Sugar()

	// 对每个新订阅的合约 symbol 设置杠杆为 DefaultContractLeverage
	for _, symbol := range futuresSymbols {
		if err := g.setLeverage(symbol, constants.DefaultContractLeverage); err != nil {
			logInstance.Debugf("set leverage for %s: %v", symbol, err)
		}
	}

	// 1. 合并到已订阅集合并维护映射
	for _, symbol := range spotSymbols {
		g.subscribedSpotSymbols[symbol] = true
		normalized := normalizeGateSymbol(symbol)
		g.gateSymbolToOriginal[normalized] = symbol
	}
	for _, symbol := range futuresSymbols {
		g.subscribedFuturesSymbols[symbol] = true
		normalized := normalizeGateSymbol(symbol)
		g.gateSymbolToOriginal[normalized] = symbol
	}

	// 2. 用完整列表重新订阅现货
	if len(g.subscribedSpotSymbols) > 0 {
		normalizedSpot := make([]string, 0, len(g.subscribedSpotSymbols))
		for symbol := range g.subscribedSpotSymbols {
			normalizedSpot = append(normalizedSpot, normalizeGateSymbol(symbol))
		}
		if err := g.wsSpotService.Subscribe(gate.ChannelSpotBookTicker, normalizedSpot); err != nil {
			logInstance.Errorf("Failed to subscribe spot symbols: %v", err)
			// 如果是连接错误，尝试重连后重新订阅
			if g.isConnectionError(err) {
				logInstance.Warn("检测到连接错误，尝试重连现货 WebSocket...")
				g.mu.Unlock() // 临时释放锁，避免死锁
				if reconnectErr := g.reconnectWebSocket("spot"); reconnectErr == nil {
					g.mu.Lock()
					// 重连后重新构建订阅列表（可能已更新）
					normalizedSpotRetry := make([]string, 0, len(g.subscribedSpotSymbols))
					for symbol := range g.subscribedSpotSymbols {
						normalizedSpotRetry = append(normalizedSpotRetry, normalizeGateSymbol(symbol))
					}
					// 重新订阅
					if retryErr := g.wsSpotService.Subscribe(gate.ChannelSpotBookTicker, normalizedSpotRetry); retryErr != nil {
						logInstance.Errorf("重连后重新订阅现货失败: %v", retryErr)
						return retryErr
					}
					logInstance.Infof("重连后成功订阅 %d 个现货 symbol", len(normalizedSpotRetry))
				} else {
					g.mu.Lock()
					return reconnectErr
				}
			} else {
				return err
			}
		} else {
			logInstance.Infof("Subscribed to %d spot symbols", len(normalizedSpot))
		}
	}

	// 3. 用完整列表重新订阅合约
	if len(g.subscribedFuturesSymbols) > 0 {
		normalizedFutures := make([]string, 0, len(g.subscribedFuturesSymbols))
		for symbol := range g.subscribedFuturesSymbols {
			normalizedFutures = append(normalizedFutures, normalizeGateSymbol(symbol))
		}
		if err := g.wsFuturesService.Subscribe(gate.ChannelFutureBookTicker, normalizedFutures); err != nil {
			logInstance.Errorf("Failed to subscribe futures symbols: %v", err)
			// 如果是连接错误，尝试重连后重新订阅
			if g.isConnectionError(err) {
				logInstance.Warn("检测到连接错误，尝试重连合约 WebSocket...")
				g.mu.Unlock() // 临时释放锁，避免死锁
				if reconnectErr := g.reconnectWebSocket("futures"); reconnectErr == nil {
					g.mu.Lock()
					// 重连后重新构建订阅列表（可能已更新）
					normalizedFuturesRetry := make([]string, 0, len(g.subscribedFuturesSymbols))
					for symbol := range g.subscribedFuturesSymbols {
						normalizedFuturesRetry = append(normalizedFuturesRetry, normalizeGateSymbol(symbol))
					}
					// 重新订阅
					if retryErr := g.wsFuturesService.Subscribe(gate.ChannelFutureBookTicker, normalizedFuturesRetry); retryErr != nil {
						logInstance.Errorf("重连后重新订阅合约失败: %v", retryErr)
						return retryErr
					}
					logInstance.Infof("重连后成功订阅 %d 个合约 symbol", len(normalizedFuturesRetry))
				} else {
					g.mu.Lock()
					return reconnectErr
				}
			} else {
				return err
			}
		} else {
			logInstance.Infof("Subscribed to %d futures symbols", len(normalizedFutures))
		}
	}

	return nil
}

// UnsubscribeTicker 取消订阅
// 从已订阅集合移除，并用剩余列表重新订阅 WS
func (g *gateExchange) UnsubscribeTicker(spotSymbols, futuresSymbols []string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.isInitialized {
		return exchange.ErrNotInitialized
	}

	logInstance := logger.GetLoggerInstance().Named("gate").Sugar()

	// 1. 从集合和映射中移除
	for _, symbol := range spotSymbols {
		delete(g.subscribedSpotSymbols, symbol)
		delete(g.gateSymbolToOriginal, normalizeGateSymbol(symbol))
	}
	for _, symbol := range futuresSymbols {
		delete(g.subscribedFuturesSymbols, symbol)
		delete(g.gateSymbolToOriginal, normalizeGateSymbol(symbol))
	}

	// 2. 用剩余列表重新订阅现货（若 Gate Subscribe 为覆盖语义，则等价于只订剩余）
	if len(g.subscribedSpotSymbols) > 0 {
		normalizedSpot := make([]string, 0, len(g.subscribedSpotSymbols))
		for symbol := range g.subscribedSpotSymbols {
			normalizedSpot = append(normalizedSpot, normalizeGateSymbol(symbol))
		}
		if err := g.wsSpotService.Subscribe(gate.ChannelSpotBookTicker, normalizedSpot); err != nil {
			logInstance.Errorf("Failed to re-subscribe spot after unsubscribe: %v", err)
			return err
		}
	}

	// 3. 用剩余列表重新订阅合约
	if len(g.subscribedFuturesSymbols) > 0 {
		normalizedFutures := make([]string, 0, len(g.subscribedFuturesSymbols))
		for symbol := range g.subscribedFuturesSymbols {
			normalizedFutures = append(normalizedFutures, normalizeGateSymbol(symbol))
		}
		if err := g.wsFuturesService.Subscribe(gate.ChannelFutureBookTicker, normalizedFutures); err != nil {
			logInstance.Errorf("Failed to re-subscribe futures after unsubscribe: %v", err)
			return err
		}
	}

	logInstance.Infof("Unsubscribed %d spot and %d futures symbols", len(spotSymbols), len(futuresSymbols))
	return nil
}

// SetTickerCallback 设置价格回调
func (g *gateExchange) SetTickerCallback(callback exchange.TickerCallback) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tickerCallback = callback
}

// PlaceOrder 下单（根据 MarketType 区分现货和合约）
func (g *gateExchange) PlaceOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	if !g.isInitialized {
		return nil, exchange.ErrNotInitialized
	}
	if req == nil {
		return nil, exchange.ErrInvalidRequest
	}

	if req.MarketType == model.MarketTypeSpot {
		return g.placeSpotOrder(req)
	}
	return g.placeFuturesOrder(req)
}

// CalculateSlippage 计算滑点
// 注意：GetBalance, GetPosition, GetPositions, GetAllBalances, GetSpotOrderBook, GetFuturesOrderBook 
// CalculateSlippage 计算滑点
func (g *gateExchange) CalculateSlippage(symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64) {
	return analytics.CalculateExchangeSlippage(g, symbol, amount, isFutures, side, slippageLimit)
}

// GetSpotBalances 获取现货账户余额（Gate 暂不支持分别获取，返回统一余额）
func (g *gateExchange) GetSpotBalances() (map[string]*model.Balance, error) {
	return g.GetAllBalances()
}

// GetFuturesBalances 获取合约账户余额（调用合约账户 API）
func (g *gateExchange) GetFuturesBalances() (map[string]*model.Balance, error) {
	g.mu.RLock()
	restClient := g.restClient
	g.mu.RUnlock()

	apiKey, secretKey := g.getAPIKeys()

	if !g.isInitialized {
		return nil, fmt.Errorf("exchange not initialized")
	}

	timestamp := getCurrentTimestamp()
	signature := signRequest("GET", constants.GateFuturesAccountPath, "", "", secretKey, timestamp)

	apiURL := fmt.Sprintf("%s%s", constants.GateRestBaseUrl, constants.GateFuturesAccountPath)
	headers := buildHeaders(apiKey, signature, timestamp)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get futures balances failed: %w", err)
	}

	var errorResp struct {
		Label   string `json:"label"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(responseBody), &errorResp) == nil && errorResp.Label != "" {
		return nil, fmt.Errorf("gate.io API error: %s - %s", errorResp.Label, errorResp.Message)
	}

	var resp struct {
		Total          string `json:"total"`
		Available      string `json:"available"`
		UnrealisedPnl  string `json:"unrealised_pnl"`
		Currency       string `json:"currency"`
		OrderMargin    string `json:"order_margin"`
		PositionMargin string `json:"position_margin"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse futures balances failed: %w, response: %s", err, responseBody)
	}

	balancesMap := make(map[string]*model.Balance)
	total, _ := strconv.ParseFloat(resp.Total, 64)
	available, _ := strconv.ParseFloat(resp.Available, 64)
	orderMargin, _ := strconv.ParseFloat(resp.OrderMargin, 64)
	positionMargin, _ := strconv.ParseFloat(resp.PositionMargin, 64)

	if total > 0 {
		coin := resp.Currency
		if coin == "" {
			coin = "USDT"
		}
		balancesMap[strings.ToUpper(coin)] = &model.Balance{
			Asset:      strings.ToUpper(coin),
			Available:  available,
			Locked:     orderMargin + positionMargin,
			Total:      total,
			UpdateTime: time.Now(),
		}
	}

	return balancesMap, nil
}

// GetQuantoMultiplier 实现 exchange.QuantoMultiplierProvider
func (g *gateExchange) GetQuantoMultiplier(symbol string) (float64, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	q, ok := g.quantoMultipliers[normalizeGateSymbol(symbol)]
	if !ok || q <= 0 {
		return 0, false
	}
	return q, true
}

// Close 关闭连接
func (g *gateExchange) Close() {
	if g.cancelFunc != nil {
		g.cancelFunc()
	}
	g.isInitialized = false
}

