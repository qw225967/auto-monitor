package okx

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/exchange"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/utils/rest"

	"github.com/qw225967/auto-monitor/internal/analytics"
)

var _ exchange.Exchange = (*okx)(nil)

const (
	spotFlag    = "spot"
	futuresFlag = "futures"
)

type okx struct {
	mu                       sync.RWMutex
	tickerCallback           exchange.TickerCallback
	subscribedSpotSymbols    map[string]bool
	subscribedFuturesSymbols map[string]bool
	isInitialized            bool
	restClient               rest.RestClient

	// WebSocket 公开行情（现货+合约共用一条连接）
	wsConn   *okxWsConn
	wsCtx    context.Context
	wsCancel context.CancelFunc
}

// NewOkx 创建 OKX 交易所实例（API 密钥从全局配置获取）
func NewOkx() exchange.Exchange {
	ctx, cancel := context.WithCancel(context.Background())
	return &okx{
		subscribedSpotSymbols:    make(map[string]bool),
		subscribedFuturesSymbols: make(map[string]bool),
		isInitialized:            false,
		wsCtx:                    ctx,
		wsCancel:                 cancel,
	}
}

// getAPIKeys 获取 API 密钥（总是从全局配置读取最新值）
// 优先使用 OKX 配置段；若为空则回退到旧的 OkEx.KeyList[0]
// 返回: apiKey, secretKey, passphrase
func (o *okx) getAPIKeys() (string, string, string) {
	globalConfig := config.GetGlobalConfig()
	if globalConfig == nil {
		return "", "", ""
	}
	// 优先使用新的 OKX 配置
	if globalConfig.OKX.APIKey != "请添加" && globalConfig.OKX.APIKey != "" && globalConfig.OKX.Secret != "请添加" && globalConfig.OKX.Secret != "" {
		return globalConfig.OKX.APIKey, globalConfig.OKX.Secret, globalConfig.OKX.Passphrase
	}
	// 回退：兼容旧的 OkEx.KeyList 配置
	if len(globalConfig.OkEx.KeyList) > 0 {
		keyRecord := globalConfig.OkEx.KeyList[0]
		return keyRecord.APIKey, keyRecord.Secret, keyRecord.Passphrase
	}
	return "", "", ""
}

// GetType 获取交易所类型
func (o *okx) GetType() string {
	return constants.ConnectTypeOKEX
}

// Init 初始化（REST 客户端；WebSocket 在 SubscribeTicker 时建立）
func (o *okx) Init() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.isInitialized {
		return nil
	}

	o.restClient.InitRestClient()
	o.isInitialized = true
	return nil
}

// ReinitRESTClients 重新初始化 REST 客户端（配置更新后调用）
func (o *okx) ReinitRESTClients() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.isInitialized {
		return exchange.ErrNotInitialized
	}
	o.restClient.InitRestClient()
	return nil
}

// SubscribeTicker 订阅行情（步骤 3 在 websocket.go 中实现，当前返回 ErrNotSupported）
func (o *okx) SubscribeTicker(spotSymbols, futureSymbols []string) error {
	return o.subscribeTicker(spotSymbols, futureSymbols)
}

// UnsubscribeTicker 取消订阅（步骤 3 实现）
func (o *okx) UnsubscribeTicker(spotSymbols, futureSymbols []string) error {
	return o.unsubscribeTicker(spotSymbols, futureSymbols)
}

// SetTickerCallback 设置行情回调
func (o *okx) SetTickerCallback(callback exchange.TickerCallback) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.tickerCallback = callback
}

// subscribeTicker 合并订阅列表，关闭旧连接后使用新 instIds 建立公开行情 WS
func (o *okx) subscribeTicker(spotSymbols, futureSymbols []string) error {
	// 为新订阅的合约 symbol 设置杠杆
	for _, s := range futureSymbols {
		if err := o.setLeverage(s, constants.DefaultContractLeverage); err != nil {
			// 杠杆设置失败不阻塞订阅（可能是已有持仓时重复设置）
			_ = err
		}
	}

	o.mu.Lock()
	for _, s := range spotSymbols {
		o.subscribedSpotSymbols[s] = true
	}
	for _, s := range futureSymbols {
		o.subscribedFuturesSymbols[s] = true
	}
	instIds := o.buildTickerInstIdsLocked()
	oldConn := o.wsConn
	o.wsConn = nil
	o.mu.Unlock()
	if oldConn != nil {
		oldConn.close()
	}
	if len(instIds) > 0 {
		go o.runWsPublic(instIds)
	}
	return nil
}

// setLeverage 设置合约杠杆（POST /api/v5/account/set-leverage）
func (o *okx) setLeverage(symbol string, leverage int) error {
	apiKey, secretKey, passphrase := o.getAPIKeys()
	instId := ToOKXSwapInstId(symbol)

	body := map[string]interface{}{
		"instId":  instId,
		"lever":   strconv.Itoa(leverage),
		"mgnMode": "cross",
	}
	bodyBytes, _ := json.Marshal(body)
	bodyStr := string(bodyBytes)

	timestamp := GetOKXTimestamp()
	sign := BuildOKXSign(timestamp, "POST", constants.OkexPathAccountSetLeverage, bodyStr, secretKey)
	headers := BuildOKXHeaders(apiKey, sign, timestamp, passphrase)

	apiURL := constants.OkexBaseUrl + constants.OkexPathAccountSetLeverage

	o.mu.RLock()
	restClient := o.restClient
	o.mu.RUnlock()

	responseBody, err := restClient.DoPostWithHeaders(apiURL, bodyStr, headers)
	if err != nil {
		return fmt.Errorf("okx set leverage: %w", err)
	}
	return CheckOKXAPIError(responseBody)
}

// unsubscribeTicker 从订阅集合移除，关闭旧连接后若仍有订阅则重连
func (o *okx) unsubscribeTicker(spotSymbols, futureSymbols []string) error {
	o.mu.Lock()
	for _, s := range spotSymbols {
		delete(o.subscribedSpotSymbols, s)
	}
	for _, s := range futureSymbols {
		delete(o.subscribedFuturesSymbols, s)
	}
	instIds := o.buildTickerInstIdsLocked()
	oldConn := o.wsConn
	o.wsConn = nil
	o.mu.Unlock()
	if oldConn != nil {
		oldConn.close()
	}
	if len(instIds) > 0 {
		go o.runWsPublic(instIds)
	}
	return nil
}

// buildTickerInstIdsLocked 在已持锁下生成当前订阅的 OKX instId 列表（现货 + 永续）
func (o *okx) buildTickerInstIdsLocked() []string {
	var instIds []string
	for s := range o.subscribedSpotSymbols {
		instIds = append(instIds, ToOKXSpotInstId(s))
	}
	for s := range o.subscribedFuturesSymbols {
		instIds = append(instIds, ToOKXSwapInstId(s))
	}
	return instIds
}

func (o *okx) CalculateSlippage(symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64) {
	return analytics.CalculateExchangeSlippage(o, symbol, amount, isFutures, side, slippageLimit)
}
