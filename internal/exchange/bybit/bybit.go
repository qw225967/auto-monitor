package bybit

import (
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

	bybit_connector "github.com/bybit-exchange/bybit.go.api"
)

var _ exchange.Exchange = (*bybit)(nil)

const (
	spotFlag    = "spot"
	futuresFlag = "futures"
)

type bybit struct {
	mu sync.RWMutex

	// 回调函数
	tickerCallback exchange.TickerCallback

	// WebSocket 客户端（分现货和合约）
	wsSpotClient    *bybit_connector.WebSocket
	wsFuturesClient *bybit_connector.WebSocket

	// HTTP REST 客户端
	restClient rest.RestClient

	// 订阅管理
	subscribedSpotSymbols    map[string]bool
	subscribedFuturesSymbols map[string]bool
	isInitialized            bool
}

// NewBybit 创建 Bybit 交易所实例（API 密钥从全局配置获取）
func NewBybit() exchange.Exchange {
	return &bybit{
		subscribedSpotSymbols:    make(map[string]bool),
		subscribedFuturesSymbols: make(map[string]bool),
		isInitialized:            false,
	}
}

// getAPIKeys 获取 API 密钥（总是从全局配置读取最新值）
func (b *bybit) getAPIKeys() (string, string) {
	globalConfig := config.GetGlobalConfig()
	if globalConfig != nil {
		return globalConfig.Bybit.APIKey, globalConfig.Bybit.Secret
	}
	return "", ""
}

// GetType 获取交易所类型
func (b *bybit) GetType() string {
	return constants.ConnectTypeBybit
}

// Init 初始化
func (b *bybit) Init() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.isInitialized {
		return nil
	}

	log := logger.GetLoggerInstance().Named("bybit").Sugar()

	// 初始化现货 WebSocket（依赖 SDK 自动重连）
	spotWs := bybit_connector.NewBybitPublicWebSocket(
		bybit_connector.SPOT_MAINNET,
		func(message string) error {
			b.handleTickerUpdate(message, spotFlag)
			return nil
		},
	)
	b.wsSpotClient = spotWs.Connect()

	// 初始化合约 WebSocket（依赖 SDK 自动重连）
	futuresWs := bybit_connector.NewBybitPublicWebSocket(
		bybit_connector.LINEAR_MAINNET,
		func(message string) error {
			b.handleTickerUpdate(message, futuresFlag)
			return nil
		},
	)
	b.wsFuturesClient = futuresWs.Connect()

	// 初始化 REST 客户端
	b.restClient.InitRestClient()

	log.Info("Bybit exchange initialized successfully")
	b.isInitialized = true
	return nil
}

// SubscribeTicker 订阅 ticker 价格数据
func (b *bybit) SubscribeTicker(spotSymbols, futuresSymbols []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.isInitialized {
		return exchange.ErrNotInitialized
	}

	log := logger.GetLoggerInstance().Named("bybit").Sugar()

	// 对每个新订阅的合约 symbol 设置杠杆为 DefaultContractLeverage
	for _, s := range futuresSymbols {
		if err := b.setLeverage(s, constants.DefaultContractLeverage); err != nil {
			log.Debugf("set leverage for %s: %v", s, err)
		}
	}

	// 订阅现货
	if len(spotSymbols) > 0 && b.wsSpotClient != nil {
		for _, symbol := range spotSymbols {
			normalizedSymbol := normalizeBybitSymbol(symbol)
			topics := []string{"orderbook.rpi." + normalizedSymbol}
			
			_, err := b.wsSpotClient.SendSubscription(topics)
			if err != nil {
				log.Errorf("Failed to subscribe spot symbol %s: %v", symbol, err)
				continue
			}
			
			b.subscribedSpotSymbols[symbol] = true
			log.Infof("Subscribed to spot symbol: %s", symbol)
		}
	}

	// 订阅合约
	if len(futuresSymbols) > 0 && b.wsFuturesClient != nil {
		for _, symbol := range futuresSymbols {
			normalizedSymbol := normalizeBybitSymbol(symbol)
			topics := []string{"orderbook.rpi." + normalizedSymbol}
			
			_, err := b.wsFuturesClient.SendSubscription(topics)
			if err != nil {
				log.Errorf("Failed to subscribe futures symbol %s: %v", symbol, err)
				continue
			}
			
			b.subscribedFuturesSymbols[symbol] = true
			log.Infof("Subscribed to futures symbol: %s", symbol)
		}
	}

	return nil
}

// UnsubscribeTicker 取消订阅
func (b *bybit) UnsubscribeTicker(spotSymbols, futuresSymbols []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.isInitialized {
		return exchange.ErrNotInitialized
	}

	// 清理订阅记录
	for _, symbol := range spotSymbols {
		delete(b.subscribedSpotSymbols, symbol)
	}
	for _, symbol := range futuresSymbols {
		delete(b.subscribedFuturesSymbols, symbol)
	}

	// 注意：Bybit SDK 可能不支持动态取消订阅，这里只清理记录
	log := logger.GetLoggerInstance().Named("bybit").Sugar()
	log.Infof("Unsubscribed %d spot and %d futures symbols", len(spotSymbols), len(futuresSymbols))

	return nil
}

// SetTickerCallback 设置价格回调
func (b *bybit) SetTickerCallback(callback exchange.TickerCallback) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tickerCallback = callback
}

// PlaceOrder 下单（根据 MarketType 区分现货和合约）
func (b *bybit) PlaceOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
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
func (b *bybit) CalculateSlippage(symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64) {
	return analytics.CalculateExchangeSlippage(b, symbol, amount, isFutures, side, slippageLimit)
}

// GetSpotBalances 获取现货账户余额（Bybit 暂不支持分别获取，返回统一余额）
func (b *bybit) GetSpotBalances() (map[string]*model.Balance, error) {
	return b.getBalancesByAccountType("SPOT")
}

// GetFuturesBalances 获取合约账户余额（使用 CONTRACT 账户类型，回退到 UNIFIED）
func (b *bybit) GetFuturesBalances() (map[string]*model.Balance, error) {
	return b.getBalancesByAccountType("CONTRACT")
}

// getBalancesByAccountType 按账户类型查询余额，查询失败时回退到 UNIFIED
func (b *bybit) getBalancesByAccountType(accountType string) (map[string]*model.Balance, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	apiKey, secretKey := b.getAPIKeys()

	if !b.isInitialized {
		return nil, fmt.Errorf("exchange not initialized")
	}

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"

	params := map[string]string{
		"accountType": accountType,
	}

	queryString := buildQueryString(params)
	signature := signRequest(timestamp, apiKey, recvWindow, queryString, secretKey)

	apiURL := fmt.Sprintf("%s%s?%s",
		constants.BybitRestBaseUrl,
		constants.BybitAccountBalancePath,
		queryString)

	headers := buildHeaders(apiKey, timestamp, signature, recvWindow)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		if accountType != "UNIFIED" {
			return b.GetAllBalances()
		}
		return nil, fmt.Errorf("get %s balances failed: %w", accountType, err)
	}

	var balanceResp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				Coin []struct {
					Coin                string `json:"coin"`
					WalletBalance       string `json:"walletBalance"`
					AvailableToWithdraw string `json:"availableToWithdraw"`
					Locked              string `json:"locked"`
				} `json:"coin"`
			} `json:"list"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(responseBody), &balanceResp); err != nil {
		if accountType != "UNIFIED" {
			return b.GetAllBalances()
		}
		return nil, fmt.Errorf("parse %s balance response failed: %w", accountType, err)
	}

	if balanceResp.RetCode != 0 {
		if accountType != "UNIFIED" {
			return b.GetAllBalances()
		}
		return nil, fmt.Errorf("bybit API error: code=%d, msg=%s", balanceResp.RetCode, balanceResp.RetMsg)
	}

	balancesMap := make(map[string]*model.Balance)
	for _, account := range balanceResp.Result.List {
		for _, coinInfo := range account.Coin {
			walletBalance, _ := strconv.ParseFloat(coinInfo.WalletBalance, 64)
			available, _ := strconv.ParseFloat(coinInfo.AvailableToWithdraw, 64)
			locked, _ := strconv.ParseFloat(coinInfo.Locked, 64)

			if walletBalance > 0 {
				balancesMap[coinInfo.Coin] = &model.Balance{
					Asset:      coinInfo.Coin,
					Available:  available,
					Locked:     locked,
					Total:      walletBalance,
					UpdateTime: time.Now(),
				}
			}
		}
	}

	if len(balancesMap) == 0 && accountType != "UNIFIED" {
		return b.GetAllBalances()
	}

	return balancesMap, nil
}
