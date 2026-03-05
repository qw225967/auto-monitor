package aster

import (
	"sync"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/analytics"
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/exchange"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/utils/logger"
	"github.com/qw225967/auto-monitor/internal/utils/rest"
)

var _ exchange.Exchange = (*aster)(nil)

type aster struct {
	mu                       sync.RWMutex
	tickerCallback           exchange.TickerCallback
	subscribedSpotSymbols    map[string]bool
	subscribedFuturesSymbols map[string]bool
	isInitialized            bool
	apiKey                   string
	secretKey                string

	restClient rest.RestClient
}

// NewAster 创建 Aster DEX 实例
func NewAster(apiKey, secretKey string) exchange.Exchange {
	return &aster{
		apiKey:                   apiKey,
		secretKey:                secretKey,
		subscribedSpotSymbols:    make(map[string]bool),
		subscribedFuturesSymbols: make(map[string]bool),
		isInitialized:            false,
	}
}

func (a *aster) GetType() string {
	return constants.ConnectTypeAster
}

func (a *aster) Init() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.isInitialized {
		return nil
	}

	// 初始化 REST 客户端
	a.restClient.InitRestClient()

	logger.GetLoggerInstance().Named("aster").Sugar().Info("Aster DEX initialized successfully")

	a.isInitialized = true
	return nil
}

// SubscribeTicker 订阅 ticker
func (a *aster) SubscribeTicker(spotSymbols, futuresSymbols []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.isInitialized {
		return exchange.ErrNotInitialized
	}

	for _, symbol := range spotSymbols {
		a.subscribedSpotSymbols[symbol] = true
	}
	for _, symbol := range futuresSymbols {
		a.subscribedFuturesSymbols[symbol] = true
	}

	logger.GetLoggerInstance().Named("aster").Sugar().Infof(
		"Subscribed to %d spot and %d futures symbols",
		len(spotSymbols), len(futuresSymbols))

	return nil
}

// UnsubscribeTicker 取消订阅
func (a *aster) UnsubscribeTicker(spotSymbols, futuresSymbols []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, symbol := range spotSymbols {
		delete(a.subscribedSpotSymbols, symbol)
	}
	for _, symbol := range futuresSymbols {
		delete(a.subscribedFuturesSymbols, symbol)
	}

	return nil
}

// SetTickerCallback 设置ticker回调
func (a *aster) SetTickerCallback(callback exchange.TickerCallback) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tickerCallback = callback
}

// PlaceOrder 下单
func (a *aster) PlaceOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	a.mu.RLock()
	if !a.isInitialized {
		a.mu.RUnlock()
		return nil, exchange.ErrNotInitialized
	}
	a.mu.RUnlock()

	return a.placeOrderInternal(req)
}

// GetBalance 获取余额
func (a *aster) GetBalance() (*model.Balance, error) {
	a.mu.RLock()
	if !a.isInitialized {
		a.mu.RUnlock()
		return nil, exchange.ErrNotInitialized
	}
	a.mu.RUnlock()

	return a.getAccountBalance()
}

// GetPosition 获取持仓
func (a *aster) GetPosition(symbol string) (*model.Position, error) {
	a.mu.RLock()
	if !a.isInitialized {
		a.mu.RUnlock()
		return nil, exchange.ErrNotInitialized
	}
	a.mu.RUnlock()

	return a.getPositionBySymbol(symbol)
}

// GetPositions 获取所有持仓
func (a *aster) GetPositions() ([]*model.Position, error) {
	a.mu.RLock()
	if !a.isInitialized {
		a.mu.RUnlock()
		return nil, exchange.ErrNotInitialized
	}
	a.mu.RUnlock()

	return a.getAllPositions()
}

// GetAllBalances 获取所有余额
func (a *aster) GetAllBalances() (map[string]*model.Balance, error) {
	a.mu.RLock()
	if !a.isInitialized {
		a.mu.RUnlock()
		return nil, exchange.ErrNotInitialized
	}
	a.mu.RUnlock()

	return a.getAllAccountBalances()
}

// GetSpotBalances 获取现货账户余额（Aster 暂不支持分别获取，返回统一余额）
func (a *aster) GetSpotBalances() (map[string]*model.Balance, error) {
	return a.GetAllBalances()
}

// GetFuturesBalances 获取合约账户余额（Aster 暂不支持分别获取，返回统一余额）
func (a *aster) GetFuturesBalances() (map[string]*model.Balance, error) {
	return a.GetAllBalances()
}

// GetSpotOrderBook 获取现货订单簿
func (a *aster) GetSpotOrderBook(symbol string) (bids [][]string, asks [][]string, err error) {
	return a.getOrderBook(symbol, false)
}

// GetFuturesOrderBook 获取合约订单簿
func (a *aster) GetFuturesOrderBook(symbol string) (bids [][]string, asks [][]string, err error) {
	return a.getOrderBook(symbol, true)
}

// CalculateSlippage 计算滑点
func (a *aster) CalculateSlippage(symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64) {
	return analytics.CalculateExchangeSlippage(a, symbol, amount, isFutures, side, slippageLimit)
}

// getAPIKeys 获取 API Keys
func (a *aster) getAPIKeys() (string, string) {
	if globalConfig := config.GetGlobalConfig(); globalConfig != nil {
		return globalConfig.Aster.APIKey, globalConfig.Aster.Secret
	}
	return a.apiKey, a.secretKey
}
