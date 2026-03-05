package bitget

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/analytics"
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/utils/rest"
)

var _ exchange.Exchange = (*bitgetExchange)(nil)

const (
	spotFlag    = "spot"
	futuresFlag = "futures"

	MaxNotReceiveDataReConnectTime = 30 // 30 秒未收到数据则重连（V2 ticker 更新可能较慢）
	MaxReconnectAttempts           = 10 // 最大重连次数
	ReconnectBackoffBase           = 2 * time.Second
)

type bitgetExchange struct {
	mu sync.RWMutex

	// WebSocket 客户端（使用本地 ws.WebSocket）
	wsConn          WebSocketConn
	isWsConnected   bool
	lastMessageTime time.Time

	// 回调函数
	tickerCallback exchange.TickerCallback

	// HTTP REST 客户端
	restClient rest.RestClient

	// 订阅管理
	subscribedSpotSymbols    map[string]bool
	subscribedFuturesSymbols map[string]bool
	isInitialized            bool

	// 上下文管理
	ctx        context.Context
	cancelFunc context.CancelFunc

	// 重连机制
	reconnectCount    int
	reconnecting      bool
	reconnectStopChan chan struct{}
}

// WebSocketConn WebSocket 连接接口（抽象化以便测试）
type WebSocketConn interface {
	Send(message string) error
	Close() error
	OnMessage(handler func(message string))
	OnDisconnected(handler func())
}

// NewBitget 创建 Bitget 交易所实例（API 密钥从全局配置获取）
func NewBitget() exchange.Exchange {
	ctx, cancel := context.WithCancel(context.Background())
	return &bitgetExchange{
		subscribedSpotSymbols:    make(map[string]bool),
		subscribedFuturesSymbols: make(map[string]bool),
		isInitialized:            false,
		ctx:                      ctx,
		cancelFunc:               cancel,
		reconnectStopChan:        make(chan struct{}),
		lastMessageTime:          time.Now(),
	}
}

// getAPIKeys 获取 API 密钥（总是从全局配置读取最新值）
func (b *bitgetExchange) getAPIKeys() (string, string, string) {
	globalConfig := config.GetGlobalConfig()
	if globalConfig != nil {
		return globalConfig.BitGet.APIKey, globalConfig.BitGet.Secret, globalConfig.BitGet.Passphrase
	}
	return "", "", ""
}

// GetType 获取交易所类型
func (b *bitgetExchange) GetType() string {
	return constants.ConnectTypeBitget
}

// Init 初始化
func (b *bitgetExchange) Init() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.isInitialized {
		return nil
	}

	logInstance := logger.GetLoggerInstance().Named("bitget").Sugar()

	// 初始化 HTTP REST 客户端（使用本地 SDK）
	b.restClient.InitRestClient()

	// 初始化 WebSocket 并登录
	if err := b.initWebSocket(); err != nil {
		return fmt.Errorf("init websocket failed: %w", err)
	}

	// 启动监控协程（检测超时和自动重连）
	go b.monitorConnection()

	logInstance.Info("Bitget exchange initialized successfully")
	b.isInitialized = true
	return nil
}

// initWebSocket 初始化 WebSocket 连接并登录
func (b *bitgetExchange) initWebSocket() error {
	// 创建 WebSocket 连接
	wsConn, err := newSimpleWebSocketConn(constants.BitgetWsUrl)
	if err != nil {
		return fmt.Errorf("failed to create websocket connection: %w", err)
	}

	// 设置回调
	wsConn.OnMessage(b.handleWebSocketMessage)
	wsConn.OnDisconnected(b.handleDisconnected)

	b.wsConn = wsConn
	b.isWsConnected = true

	// 登录
	return b.loginWebSocket()
}

// loginWebSocket 登录 WebSocket
func (b *bitgetExchange) loginWebSocket() error {
	apiKey, secretKey, passphrase := b.getAPIKeys()
	loginMsg := buildLoginMessage(apiKey, secretKey, passphrase)
	if b.wsConn != nil {
		return b.wsConn.Send(loginMsg)
	}
	return fmt.Errorf("websocket not initialized")
}

// monitorConnection 监控 WebSocket 连接状态
func (b *bitgetExchange) monitorConnection() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	logInstance := logger.GetLoggerInstance().Named("bitget.monitor").Sugar()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-b.reconnectStopChan:
			return
		case <-ticker.C:
			b.mu.RLock()
			lastMsg := b.lastMessageTime
			isConnected := b.isWsConnected
			reconnecting := b.reconnecting
			b.mu.RUnlock()

			// 如果超过阈值未收到消息且未在重连中，则触发重连
			if time.Since(lastMsg) > MaxNotReceiveDataReConnectTime*time.Second && isConnected && !reconnecting {
				logInstance.Warn("WebSocket connection timeout, triggering reconnect")
				go b.reconnect()
			}
		}
	}
}

// reconnect 重连逻辑（带指数退避）
func (b *bitgetExchange) reconnect() {
	b.mu.Lock()
	if b.reconnecting {
		b.mu.Unlock()
		return
	}
	b.reconnecting = true
	b.isWsConnected = false
	b.mu.Unlock()

	logInstance := logger.GetLoggerInstance().Named("bitget.reconnect").Sugar()
	logInstance.Info("Starting reconnection process")

	defer func() {
		b.mu.Lock()
		b.reconnecting = false
		b.mu.Unlock()
	}()

	// 关闭旧连接
	if b.wsConn != nil {
		_ = b.wsConn.Close()
	}

	for attempt := 1; attempt <= MaxReconnectAttempts; attempt++ {
		select {
		case <-b.ctx.Done():
			return
		default:
		}

		logInstance.Infof("Reconnection attempt %d/%d", attempt, MaxReconnectAttempts)

		// 初始化新的 WebSocket 连接
		if err := b.initWebSocket(); err != nil {
			logInstance.Errorf("Reconnection attempt %d failed: %v", attempt, err)
		} else {
			// 重新订阅
			if err := b.resubscribeAll(); err != nil {
				logInstance.Errorf("Resubscription failed: %v", err)
			} else {
				b.mu.Lock()
				b.isWsConnected = true
				b.lastMessageTime = time.Now()
				b.reconnectCount = 0
				b.mu.Unlock()

				logInstance.Info("Reconnection successful")
				return
			}
		}

		// 指数退避
		backoff := ReconnectBackoffBase * time.Duration(1<<uint(attempt-1))
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		logInstance.Infof("Waiting %v before next attempt", backoff)
		time.Sleep(backoff)
	}

	logInstance.Error("Max reconnection attempts reached, giving up")
}

// resubscribeAll 重新订阅所有频道
func (b *bitgetExchange) resubscribeAll() error {
	b.mu.RLock()
	spotSymbols := make([]string, 0, len(b.subscribedSpotSymbols))
	for symbol := range b.subscribedSpotSymbols {
		spotSymbols = append(spotSymbols, symbol)
	}
	futuresSymbols := make([]string, 0, len(b.subscribedFuturesSymbols))
	for symbol := range b.subscribedFuturesSymbols {
		futuresSymbols = append(futuresSymbols, symbol)
	}
	b.mu.RUnlock()

	return b.subscribeInternal(spotSymbols, futuresSymbols)
}

// SubscribeTicker 订阅 ticker 价格数据
// 将新 symbol 合并到已订阅集合，并用完整列表重新订阅，保证多 symbol 同时订阅不互相覆盖
func (b *bitgetExchange) SubscribeTicker(spotSymbols, futuresSymbols []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.isInitialized {
		return exchange.ErrNotInitialized
	}

	logInstance := logger.GetLoggerInstance().Named("bitget").Sugar()
	// 对每个新订阅的合约 symbol 设置杠杆为 DefaultContractLeverage
	for _, s := range futuresSymbols {
		if err := b.setLeverage(s, constants.DefaultContractLeverage); err != nil {
			logInstance.Debugf("set leverage for %s: %v", s, err)
		}
	}

	// 1. 合并到已订阅集合
	for _, s := range spotSymbols {
		b.subscribedSpotSymbols[s] = true
	}
	for _, s := range futuresSymbols {
		b.subscribedFuturesSymbols[s] = true
	}

	// 2. 用完整列表订阅（subscribeInternal 内部会发送 WS 消息）
	allSpot := make([]string, 0, len(b.subscribedSpotSymbols))
	for s := range b.subscribedSpotSymbols {
		allSpot = append(allSpot, s)
	}
	allFutures := make([]string, 0, len(b.subscribedFuturesSymbols))
	for s := range b.subscribedFuturesSymbols {
		allFutures = append(allFutures, s)
	}
	return b.subscribeInternal(allSpot, allFutures)
}

// subscribeInternal 内部订阅逻辑
func (b *bitgetExchange) subscribeInternal(spotSymbols, futuresSymbols []string) error {
	logInstance := logger.GetLoggerInstance().Named("bitget").Sugar()

	// 订阅现货 ticker
	if len(spotSymbols) > 0 {
		subMsg := buildSubscribeMessage(spotSymbols, "spot", "ticker")
		if err := b.wsConn.Send(subMsg); err != nil {
			logInstance.Errorf("Failed to subscribe spot symbols: %v", err)
			return err
		}
		logInstance.Infof("Subscribed to %d spot ticker symbols", len(spotSymbols))
	}

	// 订阅合约 ticker
	if len(futuresSymbols) > 0 {
		subMsg := buildSubscribeMessage(futuresSymbols, "futures", "ticker")
		if err := b.wsConn.Send(subMsg); err != nil {
			logInstance.Errorf("Failed to subscribe futures symbols: %v", err)
			return err
		}
		logInstance.Infof("Subscribed to %d futures ticker symbols", len(futuresSymbols))
	}

	return nil
}

// UnsubscribeTicker 取消订阅
// 从已订阅集合移除，并用剩余列表重新订阅
func (b *bitgetExchange) UnsubscribeTicker(spotSymbols, futuresSymbols []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.isInitialized {
		return exchange.ErrNotInitialized
	}

	logInstance := logger.GetLoggerInstance().Named("bitget").Sugar()

	for _, s := range spotSymbols {
		delete(b.subscribedSpotSymbols, s)
	}
	for _, s := range futuresSymbols {
		delete(b.subscribedFuturesSymbols, s)
	}

	// 用剩余列表重新订阅
	allSpot := make([]string, 0, len(b.subscribedSpotSymbols))
	for s := range b.subscribedSpotSymbols {
		allSpot = append(allSpot, s)
	}
	allFutures := make([]string, 0, len(b.subscribedFuturesSymbols))
	for s := range b.subscribedFuturesSymbols {
		allFutures = append(allFutures, s)
	}
	if err := b.subscribeInternal(allSpot, allFutures); err != nil {
		logInstance.Errorf("Re-subscribe after unsubscribe failed: %v", err)
		return err
	}

	logInstance.Infof("Unsubscribed %d spot and %d futures symbols", len(spotSymbols), len(futuresSymbols))
	return nil
}

// SetTickerCallback 设置价格回调
func (b *bitgetExchange) SetTickerCallback(callback exchange.TickerCallback) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tickerCallback = callback
}

// PlaceOrder 下单（根据 MarketType 区分现货和合约）
func (b *bitgetExchange) PlaceOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}
	if req == nil {
		return nil, exchange.ErrInvalidRequest
	}

	if req.MarketType == model.MarketTypeSpot {
		return b.placeSpotOrder(req)
	}
	return b.placeFuturesOrder(req)
}

// CalculateSlippage 计算滑点
// 注意：GetBalance, GetPosition, GetPositions, GetAllBalances, GetSpotOrderBook, GetFuturesOrderBook
// CalculateSlippage 计算滑点
func (b *bitgetExchange) CalculateSlippage(symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64) {
	return analytics.CalculateExchangeSlippage(b, symbol, amount, isFutures, side, slippageLimit)
}

// GetSpotBalances 获取现货账户余额（Bitget 暂不支持分别获取，返回统一余额）
func (b *bitgetExchange) GetSpotBalances() (map[string]*model.Balance, error) {
	return b.GetAllBalances()
}

// GetFuturesBalances 获取合约账户余额（调用合约账户 API）
func (b *bitgetExchange) GetFuturesBalances() (map[string]*model.Balance, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	apiKey, secretKey, passphrase := b.getAPIKeys()

	if !b.isInitialized {
		return nil, fmt.Errorf("exchange not initialized")
	}

	queryString := "productType=USDT-FUTURES"
	timestamp := getCurrentTimestamp()
	signature := signRequest(timestamp, "GET", constants.BitgetFuturesAccount, queryString, "", secretKey)

	apiURL := fmt.Sprintf("%s%s?%s", constants.BitgetRestBaseUrl, constants.BitgetFuturesAccount, queryString)
	headers := buildHeaders(apiKey, signature, timestamp, passphrase)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get futures balances failed: %w", err)
	}

	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MarginCoin       string `json:"marginCoin"`
			AccountEquity    string `json:"accountEquity"`
			Available        string `json:"available"`
			Locked           string `json:"locked"`
			UnrealisedPL     string `json:"unrealisedPL"`
			CrossedUnrealizedPL string `json:"crossedUnrealizedPL"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse futures balances failed: %w", err)
	}

	if resp.Code != "00000" {
		return nil, fmt.Errorf("bitget API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	balancesMap := make(map[string]*model.Balance)
	equity, _ := strconv.ParseFloat(resp.Data.AccountEquity, 64)
	available, _ := strconv.ParseFloat(resp.Data.Available, 64)
	locked, _ := strconv.ParseFloat(resp.Data.Locked, 64)

	if equity > 0 {
		coin := resp.Data.MarginCoin
		if coin == "" {
			coin = "USDT"
		}
		balancesMap[coin] = &model.Balance{
			Asset:      coin,
			Available:  available,
			Locked:     locked,
			Total:      equity,
			UpdateTime: time.Now(),
		}
	}

	return balancesMap, nil
}

// Close 关闭连接
func (b *bitgetExchange) Close() {
	close(b.reconnectStopChan)
	if b.cancelFunc != nil {
		b.cancelFunc()
	}
	if b.wsConn != nil {
		_ = b.wsConn.Close()
	}
	b.isInitialized = false
}
