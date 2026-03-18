package hyperliquid

import (
	"context"
	"sync"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/analytics"
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/exchange"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/utils/logger"
	"github.com/qw225967/auto-monitor/internal/utils/rest"

	"go.uber.org/zap"
)

var _ exchange.Exchange = (*hyperliquidExchange)(nil)

type hyperliquidExchange struct {
	mu                       sync.RWMutex
	tickerCallback           exchange.TickerCallback
	subscribedSpotSymbols    map[string]bool
	subscribedFuturesSymbols map[string]bool
	isInitialized            bool
	
	// Hyperliquid 使用钱包地址和私钥进行认证
	walletAddress string
	privateKey    string // ED25519 私钥（hex 格式）
	
	restClient    rest.RestClient
	
	// WebSocket 相关
	wsConn        *websocketConn
	wsContext     context.Context
	wsCancel      context.CancelFunc
	
	// 重连相关
	reconnectContext context.Context
	reconnectCancel  context.CancelFunc
	reconnectMutex   sync.Mutex
	isReconnecting   bool
	
	logger *zap.SugaredLogger
}

// NewHyperliquid 创建 Hyperliquid 交易所实例
// userAddress: 用户的主钱包地址
// apiPrivateKey: API 钱包私钥（hex 格式，地址会自动派生）
func NewHyperliquid(userAddress, apiPrivateKey string) exchange.Exchange {
	ctx, cancel := context.WithCancel(context.Background())
	wsCtx, wsCancel := context.WithCancel(context.Background())
	
	return &hyperliquidExchange{
		walletAddress:            userAddress,      // 主钱包地址
		privateKey:               apiPrivateKey,    // API 私钥
		subscribedSpotSymbols:    make(map[string]bool),
		subscribedFuturesSymbols: make(map[string]bool),
		isInitialized:            false,
		reconnectContext:         ctx,
		reconnectCancel:          cancel,
		wsContext:                wsCtx,
		wsCancel:                 wsCancel,
		logger:                   logger.GetLoggerInstance().Named("hyperliquid").Sugar(),
	}
}

// GetType 获取交易所类型
func (h *hyperliquidExchange) GetType() string {
	return constants.ConnectTypeHyperliquid
}

// Init 初始化交易所连接
func (h *hyperliquidExchange) Init() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.isInitialized {
		return nil
	}

	// 初始化 REST 客户端
	h.restClient.InitRestClient()
	
	// 初始化 WebSocket 连接（如果需要）
	// WebSocket 将在 SubscribeTicker 时按需建立
	
	h.isInitialized = true
	h.logger.Info("Hyperliquid exchange initialized successfully")
	
	return nil
}

// getWalletCredentials 获取钱包凭证（支持从全局配置读取最新值）
func (h *hyperliquidExchange) getWalletCredentials() (string, string) {
	// 优先使用全局配置（支持动态更新）
	globalConfig := config.GetGlobalConfig()
	if globalConfig != nil && globalConfig.Hyperliquid.UserAddress != "" {
		// 返回用户主钱包地址和 API 私钥
		return globalConfig.Hyperliquid.UserAddress, globalConfig.Hyperliquid.APIPrivateKey
	}
	
	// 回退到实例变量
	return h.walletAddress, h.privateKey
}

// SubscribeTicker 订阅 ticker 价格数据
// 将新 symbol 合并到已订阅集合，并用完整列表重新订阅，保证多 symbol 同时订阅不互相覆盖
func (h *hyperliquidExchange) SubscribeTicker(spotSymbols, futureSymbols []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.isInitialized {
		return exchange.ErrNotInitialized
	}

	// 对每个新订阅的合约 symbol 设置杠杆为 DefaultContractLeverage
	for _, s := range futureSymbols {
		if err := h.setLeverage(s, constants.DefaultContractLeverage); err != nil {
			h.logger.Debugf("set leverage for %s: %v", s, err)
		}
	}

	// 1. 合并到已订阅集合
	for _, s := range spotSymbols {
		h.subscribedSpotSymbols[s] = true
	}
	for _, s := range futureSymbols {
		h.subscribedFuturesSymbols[s] = true
	}

	// 2. 初始化 WebSocket 连接（如果还没有）
	if h.wsConn == nil {
		walletAddress, privateKey := h.getWalletCredentials()
		h.wsConn = newWebsocketConn(walletAddress, privateKey, h.logger)

		h.wsConn.setTickerCallback(func(symbol string, ticker *model.Ticker) {
			if h.tickerCallback != nil {
				// Hyperliquid 只支持合约，默认使用 futures
				h.tickerCallback(symbol, ticker, "futures")
			}
		})

		if err := h.wsConn.connect(); err != nil {
			h.logger.Errorf("Failed to connect WebSocket: %v", err)
			return err
		}
	}

	// 3. 用完整列表订阅
	allSymbols := make([]string, 0, len(h.subscribedSpotSymbols)+len(h.subscribedFuturesSymbols))
	for s := range h.subscribedSpotSymbols {
		allSymbols = append(allSymbols, s)
	}
	for s := range h.subscribedFuturesSymbols {
		allSymbols = append(allSymbols, s)
	}
	if err := h.wsConn.subscribe(allSymbols); err != nil {
		h.logger.Errorf("Failed to subscribe symbols: %v", err)
		return err
	}

	h.logger.Infof("Subscribed to %d spot and %d futures symbols",
		len(h.subscribedSpotSymbols), len(h.subscribedFuturesSymbols))
	return nil
}

// UnsubscribeTicker 取消订阅 ticker 数据
func (h *hyperliquidExchange) UnsubscribeTicker(spotSymbols, futureSymbols []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.isInitialized {
		return exchange.ErrNotInitialized
	}

	// 移除订阅记录
	for _, symbol := range spotSymbols {
		delete(h.subscribedSpotSymbols, symbol)
	}
	for _, symbol := range futureSymbols {
		delete(h.subscribedFuturesSymbols, symbol)
	}
	
	// 取消订阅（如果 WebSocket 连接存在）
	if h.wsConn != nil {
		allSymbols := append(spotSymbols, futureSymbols...)
		if err := h.wsConn.unsubscribe(allSymbols); err != nil {
			h.logger.Errorf("Failed to unsubscribe symbols: %v", err)
			return err
		}
	}
	
	h.logger.Infof("Unsubscribed %d spot and %d futures symbols",
		len(spotSymbols), len(futureSymbols))
	
	return nil
}

// SetTickerCallback 设置价格数据回调函数
func (h *hyperliquidExchange) SetTickerCallback(callback exchange.TickerCallback) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tickerCallback = callback
}

// PlaceOrder 下单
func (h *hyperliquidExchange) PlaceOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	h.mu.RLock()
	isInitialized := h.isInitialized
	h.mu.RUnlock()

	if !isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	// 根据市场类型分别处理
	if req.MarketType == model.MarketTypeFutures {
		return h.placeFuturesOrder(req)
	}
	return h.placeSpotOrder(req)
}

// GetBalance 获取账户余额
func (h *hyperliquidExchange) GetBalance() (*model.Balance, error) {
	h.mu.RLock()
	isInitialized := h.isInitialized
	h.mu.RUnlock()

	if !isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	return h.getAccountBalance()
}

// GetPosition 获取指定交易对的持仓
func (h *hyperliquidExchange) GetPosition(symbol string) (*model.Position, error) {
	h.mu.RLock()
	isInitialized := h.isInitialized
	h.mu.RUnlock()

	if !isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	return h.getPositionBySymbol(symbol)
}

// GetPositions 获取所有持仓
func (h *hyperliquidExchange) GetPositions() ([]*model.Position, error) {
	h.mu.RLock()
	isInitialized := h.isInitialized
	h.mu.RUnlock()

	if !isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	return h.getAllPositions()
}

// GetAllBalances 获取所有币种的余额
func (h *hyperliquidExchange) GetAllBalances() (map[string]*model.Balance, error) {
	h.mu.RLock()
	isInitialized := h.isInitialized
	h.mu.RUnlock()

	if !isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	return h.getAllAccountBalances()
}

// GetSpotBalances 获取现货账户余额（Hyperliquid 暂不支持分别获取，返回统一余额）
func (h *hyperliquidExchange) GetSpotBalances() (map[string]*model.Balance, error) {
	return h.GetAllBalances()
}

// GetFuturesBalances 获取合约账户余额（Hyperliquid 暂不支持分别获取，返回统一余额）
func (h *hyperliquidExchange) GetFuturesBalances() (map[string]*model.Balance, error) {
	return h.GetAllBalances()
}

// GetSpotOrderBook 获取现货订单簿
func (h *hyperliquidExchange) GetSpotOrderBook(symbol string) (bids [][]string, asks [][]string, err error) {
	h.mu.RLock()
	isInitialized := h.isInitialized
	h.mu.RUnlock()

	if !isInitialized {
		return nil, nil, exchange.ErrNotInitialized
	}

	return h.getOrderBook(symbol, false)
}

// GetFuturesOrderBook 获取合约订单簿
func (h *hyperliquidExchange) GetFuturesOrderBook(symbol string) (bids [][]string, asks [][]string, err error) {
	h.mu.RLock()
	isInitialized := h.isInitialized
	h.mu.RUnlock()

	if !isInitialized {
		return nil, nil, exchange.ErrNotInitialized
	}

	return h.getOrderBook(symbol, true)
}

// CalculateSlippage 计算滑点
func (h *hyperliquidExchange) CalculateSlippage(symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64) {
	return analytics.CalculateExchangeSlippage(h, symbol, amount, isFutures, side, slippageLimit)
}

// Close 关闭交易所连接
func (h *hyperliquidExchange) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.isInitialized {
		return nil
	}

	// 关闭 WebSocket 连接
	if h.wsConn != nil {
		h.wsConn.close()
	}
	
	// 取消上下文
	if h.wsCancel != nil {
		h.wsCancel()
	}
	if h.reconnectCancel != nil {
		h.reconnectCancel()
	}
	
	h.isInitialized = false
	h.logger.Info("Hyperliquid exchange closed")
	
	return nil
}
