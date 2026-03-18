package binance

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/analytics"
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/exchange"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/utils/logger"
	"github.com/qw225967/auto-monitor/internal/utils/rest"

	binance_connector "github.com/binance/binance-connector-go"
)

var _ exchange.Exchange = (*binance)(nil)

const (
	spotFlag    = "spot"
	futuresFlag = "futures"
)

type binance struct {
	mu                       sync.RWMutex
	tickerCallback           exchange.TickerCallback
	subscribedSpotSymbols    map[string]bool
	subscribedFuturesSymbols map[string]bool
	isInitialized            bool
	apiKey                   string
	secretKey                string

	wsStreamClient        *binance_connector.WebsocketStreamClient
	wsStreamFuturesClient *binance_connector.WebsocketStreamClient

	restAPISpotClient    *binance_connector.Client
	restAPIFuturesClient *binance_connector.Client
	restClient           rest.RestClient

	spotDoneCh, spotStopCh       chan struct{}
	futuresDoneCh, futuresStopCh chan struct{}

	// 重连相关
	reconnectContext context.Context
	reconnectCancel  context.CancelFunc
	reconnectMutex   sync.Mutex
	isReconnecting   bool
}

// NewBinance 创建币安交易所实例
func NewBinance(apiKey, secretKey string) exchange.Exchange {
	ctx, cancel := context.WithCancel(context.Background())
	return &binance{
		apiKey:                   apiKey,
		secretKey:                secretKey,
		subscribedSpotSymbols:    make(map[string]bool),
		subscribedFuturesSymbols: make(map[string]bool),
		isInitialized:            false,
		reconnectContext:         ctx,
		reconnectCancel:          cancel,
	}
}

// GetType 获取交易所类型
func (b *binance) GetType() string {
	return constants.ConnectTypeBinance
}

// Init 初始化
func (b *binance) Init() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.isInitialized {
		return nil
	}

	b.wsStreamClient = binance_connector.NewWebsocketStreamClient(true)
	b.wsStreamFuturesClient = binance_connector.NewWebsocketStreamClient(true, constants.BinanceWsBaseFuturesUrl)

	// 使用 getAPIKeys() 获取 API key（支持从全局配置读取最新值）
	apiKey, secretKey := b.getAPIKeys()

	b.restAPISpotClient = binance_connector.NewClient(apiKey, secretKey, constants.BinanceRestBaseSpotUrl)
	b.restAPIFuturesClient = binance_connector.NewClient(apiKey, secretKey, constants.BinanceRestBaseFuturesUrl)
	b.restClient.InitRestClient()

	b.isInitialized = true
	return nil
}

// ReinitRESTClients 重新初始化 REST 客户端（用于配置更新后）
func (b *binance) ReinitRESTClients() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.isInitialized {
		return exchange.ErrNotInitialized
	}

	// 获取最新的 API key
	apiKey, secretKey := b.getAPIKeys()

	// 重新创建 REST 客户端
	b.restAPISpotClient = binance_connector.NewClient(apiKey, secretKey, constants.BinanceRestBaseSpotUrl)
	b.restAPIFuturesClient = binance_connector.NewClient(apiKey, secretKey, constants.BinanceRestBaseFuturesUrl)
	b.restClient.InitRestClient()

	return nil
}

// SubscribeTicker 订阅ticker价格数据
// 将新 symbol 合并到已订阅集合，并用完整列表重新建立 WebSocket，保证多 symbol 同时订阅不互相覆盖
func (b *binance) SubscribeTicker(spotSymbols, futuresSymbols []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.isInitialized {
		return exchange.ErrNotInitialized
	}

	// 0. 对每个新订阅的合约 symbol 设置杠杆为 DefaultContractLeverage（每次初始化/订阅时生效）
	log := logger.GetLoggerInstance().Named("binance").Sugar()
	for _, s := range futuresSymbols {
		if err := b.setLeverage(s, constants.DefaultContractLeverage); err != nil {
			log.Debugf("set leverage for %s: %v", s, err)
		}
	}

	// 1. 先合并到已订阅集合
	for _, s := range spotSymbols {
		b.subscribedSpotSymbols[s] = true
	}
	for _, s := range futuresSymbols {
		b.subscribedFuturesSymbols[s] = true
	}

	// 2. 用完整列表构建 spot / futures 的 slice
	allSpot := make([]string, 0, len(b.subscribedSpotSymbols))
	for s := range b.subscribedSpotSymbols {
		allSpot = append(allSpot, s)
	}
	allFutures := make([]string, 0, len(b.subscribedFuturesSymbols))
	for s := range b.subscribedFuturesSymbols {
		allFutures = append(allFutures, s)
	}

	// 3. 现货：停止旧连接后，用完整列表重新订阅
	if len(allSpot) > 0 {
		if b.spotStopCh != nil {
			oldCh := b.spotStopCh
			b.spotStopCh = nil
			go func() { oldCh <- struct{}{} }()
		}
		wsSpotHandler := func(event *binance_connector.WsBookTickerEvent) {
			b.handleTickerUpdate(event, spotFlag)
		}
		wsErrorHandler := func(err error) {
			b.handleTickerError(err)
		}
		var err error
		b.spotDoneCh, b.spotStopCh, err = b.wsStreamClient.WsCombinedBookTickerServe(allSpot, wsSpotHandler, wsErrorHandler)
		if err != nil {
			b.handleTickerError(err)
		}
	}

	// 4. 合约：停止旧连接后，用完整列表重新订阅
	if len(allFutures) > 0 {
		if b.futuresStopCh != nil {
			oldCh := b.futuresStopCh
			b.futuresStopCh = nil
			go func() { oldCh <- struct{}{} }()
		}
		wsFuturesHandler := func(event *binance_connector.WsBookTickerEvent) {
			b.handleTickerUpdate(event, futuresFlag)
		}
		wsFuturesErrorHandler := func(err error) {
			b.handleTickerError(err)
		}
		var err error
		b.futuresDoneCh, b.futuresStopCh, err = b.wsStreamFuturesClient.WsCombinedBookTickerServe(allFutures, wsFuturesHandler, wsFuturesErrorHandler)
		if err != nil {
			b.handleTickerError(err)
		}
	}

	b.startReconnectWatchers()
	return nil
}

// startReconnectWatchers 启动监听断链重连的 goroutine
func (b *binance) startReconnectWatchers() {
	// 监听现货 WebSocket 断链
	if b.spotDoneCh != nil {
		go b.watchConnection("spot")
	}

	// 监听合约 WebSocket 断链
	if b.futuresDoneCh != nil {
		go b.watchConnection("futures")
	}
}

// watchConnection 监听连接状态，断链时自动重连
func (b *binance) watchConnection(marketType string) {
	log := logger.GetLoggerInstance().Named("binance.reconnect").Sugar()

	for {
		// 获取当前的 doneCh
		b.mu.RLock()
		var doneCh chan struct{}
		if marketType == "spot" {
			doneCh = b.spotDoneCh
		} else {
			doneCh = b.futuresDoneCh
		}
		b.mu.RUnlock()

		if doneCh == nil {
			// 如果没有 doneCh，检查 context 是否已取消
			select {
			case <-b.reconnectContext.Done():
				log.Debugf("%s watchConnection: reconnectContext 已取消，退出", marketType)
				return
			case <-time.After(1 * time.Second):
				// 等待 1 秒后重试
				continue
			}
		}

		select {
		case <-b.reconnectContext.Done():
			log.Debugf("%s watchConnection: reconnectContext 已取消，退出", marketType)
			return
		case <-doneCh:
			log.Warnf("检测到 %s WebSocket 连接断开，准备重连...", marketType)

			// 检查是否正在重连，避免重复重连
			b.reconnectMutex.Lock()
			if b.isReconnecting {
				b.reconnectMutex.Unlock()
				log.Debugf("%s 正在重连中，跳过本次重连", marketType)
				// 等待重连完成后继续监听
				time.Sleep(3 * time.Second)
				continue
			}
			b.isReconnecting = true
			b.reconnectMutex.Unlock()

			// 延迟重连，避免频繁重连
			time.Sleep(2 * time.Second)

			// 执行重连
			b.reconnect(marketType)

			b.reconnectMutex.Lock()
			b.isReconnecting = false
			b.reconnectMutex.Unlock()

			// 重连后继续监听新的 doneCh
			log.Debugf("%s 重连完成，继续监听连接状态", marketType)
		}
	}
}

// reconnect 重新连接并订阅
func (b *binance) reconnect(marketType string) {
	log := logger.GetLoggerInstance().Named("binance.reconnect").Sugar()

	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.isInitialized {
		log.Warnf("%s 未初始化，无法重连", marketType)
		return
	}

	// 获取已订阅的 symbol 列表
	var spotSymbols []string
	var futuresSymbols []string

	for symbol := range b.subscribedSpotSymbols {
		spotSymbols = append(spotSymbols, symbol)
	}
	for symbol := range b.subscribedFuturesSymbols {
		futuresSymbols = append(futuresSymbols, symbol)
	}

	if len(spotSymbols) == 0 && len(futuresSymbols) == 0 {
		log.Debugf("%s 没有已订阅的 symbol，跳过重连", marketType)
		return
	}

	log.Infof("开始重连 %s WebSocket，已订阅 symbol: spot=%d, futures=%d", marketType, len(spotSymbols), len(futuresSymbols))

	// 重新创建 WebSocket 客户端
	if marketType == "spot" || len(spotSymbols) > 0 {
		b.wsStreamClient = binance_connector.NewWebsocketStreamClient(true)
	}
	if marketType == "futures" || len(futuresSymbols) > 0 {
		b.wsStreamFuturesClient = binance_connector.NewWebsocketStreamClient(true, constants.BinanceWsBaseFuturesUrl)
	}

	// 重新订阅
	var err error
	if len(spotSymbols) > 0 {
		wsSpotHandler := func(event *binance_connector.WsBookTickerEvent) {
			b.handleTickerUpdate(event, spotFlag)
		}
		wsErrorHandler := func(err error) {
			b.handleTickerError(err)
		}
		b.spotDoneCh, b.spotStopCh, err = b.wsStreamClient.WsCombinedBookTickerServe(spotSymbols, wsSpotHandler, wsErrorHandler)
		if err != nil {
			log.Errorf("重连现货 WebSocket 失败: %v", err)
		} else {
			log.Infof("现货 WebSocket 重连成功，已订阅 %d 个 symbol", len(spotSymbols))
		}
	}

	if len(futuresSymbols) > 0 {
		wsFuturesHandler := func(event *binance_connector.WsBookTickerEvent) {
			b.handleTickerUpdate(event, futuresFlag)
		}
		wsFuturesErrorHandler := func(err error) {
			b.handleTickerError(err)
		}
		b.futuresDoneCh, b.futuresStopCh, err = b.wsStreamFuturesClient.WsCombinedBookTickerServe(futuresSymbols, wsFuturesHandler, wsFuturesErrorHandler)
		if err != nil {
			log.Errorf("重连合约 WebSocket 失败: %v", err)
		} else {
			log.Infof("合约 WebSocket 重连成功，已订阅 %d 个 symbol", len(futuresSymbols))
		}
	}
}

// UnsubscribeTicker 取消订阅
// 从已订阅集合移除并停止当前连接；重连逻辑会按剩余列表重新订阅
func (b *binance) UnsubscribeTicker(spotSymbols, futuresSymbols []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.isInitialized {
		return exchange.ErrNotInitialized
	}

	for _, s := range spotSymbols {
		delete(b.subscribedSpotSymbols, s)
	}
	for _, s := range futuresSymbols {
		delete(b.subscribedFuturesSymbols, s)
	}

	// 停止现货连接，watchConnection 会在 doneCh 关闭后按剩余列表重连
	if b.spotStopCh != nil {
		oldCh := b.spotStopCh
		b.spotStopCh = nil
		go func() { oldCh <- struct{}{} }()
	}
	// 停止合约连接
	if b.futuresStopCh != nil {
		oldCh := b.futuresStopCh
		b.futuresStopCh = nil
		go func() { oldCh <- struct{}{} }()
	}

	return nil
}

// SetTickerCallback 设置价格回调
func (b *binance) SetTickerCallback(callback exchange.TickerCallback) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tickerCallback = callback
}

// PlaceOrder 下单（根据 MarketType 区分现货和合约）
func (b *binance) PlaceOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
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

// GetBalance 查询账户余额
func (b *binance) GetBalance() (*model.Balance, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	balances, err := b.getUnifiedAccountBalance()
	if err != nil {
		return nil, err
	}

	// 查找 USDT 余额
	for _, balance := range balances {
		if balance.Asset == "USDT" {
			total, _ := strconv.ParseFloat(balance.TotalWalletBalance, 64)
			available, _ := strconv.ParseFloat(balance.CrossMarginFree, 64)
			locked, _ := strconv.ParseFloat(balance.CrossMarginLocked, 64)

			updateTime := time.Unix(balance.UpdateTime/1000, 0)
			if balance.UpdateTime == 0 {
				updateTime = time.Now()
			}

			return &model.Balance{
				Asset:      "USDT",
				Available:  available,
				Locked:     locked,
				Total:      total,
				UpdateTime: updateTime,
			}, nil
		}
	}

	// 如果没有找到 USDT，返回零余额
	return &model.Balance{
		Asset:      "USDT",
		Available:  0,
		Locked:     0,
		Total:      0,
		UpdateTime: time.Now(),
	}, nil
}

// GetPosition 查询持仓
func (b *binance) GetPosition(symbol string) (*model.Position, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	if symbol == "" {
		return nil, exchange.ErrInvalidSymbol
	}

	positions, err := b.getUnifiedAccountPositionRisk(symbol)
	if err != nil {
		return nil, err
	}

	// 查找指定 symbol 的持仓
	for _, pos := range positions {
		if pos.Symbol == symbol {
			return convertPositionRiskToModel(pos), nil
		}
	}

	// 如果没有找到，返回零持仓
	return &model.Position{
		Symbol:        symbol,
		Side:          "",
		Size:          0,
		EntryPrice:    0,
		MarkPrice:     0,
		UnrealizedPnl: 0,
		Leverage:      0,
		UpdateTime:    time.Now(),
	}, nil
}

// GetPositions 查询所有持仓
func (b *binance) GetPositions() ([]*model.Position, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	positionsResp, err := b.getUnifiedAccountPositionRisk("")
	if err != nil {
		return nil, err
	}

	positions := make([]*model.Position, 0)
	for _, posResp := range positionsResp {
		positionAmt, _ := strconv.ParseFloat(posResp.PositionAmt, 64)
		if positionAmt == 0 {
			continue
		}
		positions = append(positions, convertPositionRiskToModel(posResp))
	}

	return positions, nil
}

// CalculateSlippage 计算滑点
func (b *binance) CalculateSlippage(symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64) {
	return analytics.CalculateExchangeSlippage(b, symbol, amount, isFutures, side, slippageLimit)
}

// normalizeAPIKey 去除首尾空格、BOM、引号、换行，避免 -2014
func normalizeAPIKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	s = strings.TrimPrefix(s, "\xef\xbb\xbf")
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		s = s[1 : len(s)-1]
		s = strings.TrimSpace(s)
	}
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

// getAPIKeys 优先用实例 key（构造函数传入），否则用全局配置；均做 normalize
func (b *binance) getAPIKeys() (string, string) {
	ik, is := normalizeAPIKey(b.apiKey), normalizeAPIKey(b.secretKey)
	if ik != "" {
		return ik, is
	}
	globalConfig := config.GetGlobalConfig()
	if globalConfig != nil {
		return normalizeAPIKey(globalConfig.Binance.APIKey), normalizeAPIKey(globalConfig.Binance.SecretKey)
	}
	return "", ""
}

// GetAllBalances 获取所有币种的综合余额（现货 + 合约）
// 用途：作为调试/统计的汇总视图；精确用法请优先分别调用 GetSpotBalances / GetFuturesBalances
func (b *binance) GetAllBalances() (map[string]*model.Balance, error) {
	// 这里不长期持锁，避免在调用外部 API 时阻塞其它操作
	b.mu.RLock()
	initialized := b.isInitialized
	b.mu.RUnlock()

	if !initialized {
		return nil, exchange.ErrNotInitialized
	}

	// 1. 尝试获取现货余额
	spotBalances, _ := b.getSpotAccountBalance()

	// 2. 获取统一账户余额（包含合约及部分保证金信息）
	unifiedBalances, err := b.getUnifiedAccountBalance()
	if err != nil {
		return nil, err
	}

	result := make(map[string]*model.Balance)

	// 2.1 先写入现货余额
	now := time.Now()
	for asset, bal := range spotBalances {
		if bal == nil {
			continue
		}
		// 拷贝一份，避免外部修改内部结构
		result[asset] = &model.Balance{
			Asset:      bal.Asset,
			Available:  bal.Available,
			Locked:     bal.Locked,
			Total:      bal.Total,
			UpdateTime: bal.UpdateTime,
		}
	}

	// 2.2 再叠加统一账户中的合约/保证金余额
	for _, ub := range unifiedBalances {
		if ub == nil {
			continue
		}

		totalWallet, _ := strconv.ParseFloat(ub.TotalWalletBalance, 64)
		crossFree, _ := strconv.ParseFloat(ub.CrossMarginFree, 64)
		crossLocked, _ := strconv.ParseFloat(ub.CrossMarginLocked, 64)
		umWallet, _ := strconv.ParseFloat(ub.UmWalletBalance, 64)
		cmWallet, _ := strconv.ParseFloat(ub.CmWalletBalance, 64)

		updateTime := time.Unix(ub.UpdateTime/1000, 0)
		if ub.UpdateTime == 0 {
			updateTime = now
		}

		// 如果已有现货记录，则在此基础上加上合约钱包余额（um + cm）
		if existing, ok := result[ub.Asset]; ok && existing != nil {
			existing.Total += umWallet + cmWallet
			if updateTime.After(existing.UpdateTime) {
				existing.UpdateTime = updateTime
			}
			continue
		}

		// 否则，统一账户条目本身也可能代表资产（例如全部在合约或保证金中）
		if totalWallet > 0 || crossFree > 0 || crossLocked > 0 || umWallet > 0 || cmWallet > 0 {
			result[ub.Asset] = &model.Balance{
				Asset:      ub.Asset,
				Available:  crossFree,   // 对于保证金账户，以可用保证金近似为 available
				Locked:     crossLocked, // 保证金锁定部分
				Total:      totalWallet, // 总钱包余额
				UpdateTime: updateTime,
			}
		}
	}

	return result, nil
}

// GetSpotBalances 获取现货账户余额
// 优先使用现货 REST API (/api/v3/account)，如果失败再回退到统一账户 API
func (b *binance) GetSpotBalances() (map[string]*model.Balance, error) {
	// 先检查是否已初始化（避免在持有读锁时长时间调用外部 API）
	b.mu.RLock()
	initialized := b.isInitialized
	b.mu.RUnlock()

	if !initialized {
		return nil, exchange.ErrNotInitialized
	}

	// 1. 尝试使用现货 REST API 获取余额
	if spotBalances, err := b.getSpotAccountBalance(); err == nil && len(spotBalances) > 0 {
		return spotBalances, nil
	}

	// 2. 如果现货 API 不可用或返回空结果，回退到统一账户 API
	balances, err := b.getUnifiedAccountBalance()
	if err != nil {
		return nil, err
	}

	// 使用 CrossMarginFree / CrossMarginLocked 近似构建现货余额（老逻辑，作为兜底）
	balancesMap := make(map[string]*model.Balance)
	for _, bal := range balances {
		available, _ := strconv.ParseFloat(bal.CrossMarginFree, 64)
		locked, _ := strconv.ParseFloat(bal.CrossMarginLocked, 64)
		total := available + locked

		updateTime := time.Unix(bal.UpdateTime/1000, 0)
		if bal.UpdateTime == 0 {
			updateTime = time.Now()
		}

		if total > 0 || available > 0 || locked > 0 {
			balancesMap[bal.Asset] = &model.Balance{
				Asset:      bal.Asset,
				Available:  available,
				Locked:     locked,
				Total:      total,
				UpdateTime: updateTime,
			}
		}
	}

	return balancesMap, nil
}

// GetFuturesBalances 获取合约账户余额
func (b *binance) GetFuturesBalances() (map[string]*model.Balance, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	balances, err := b.getUnifiedAccountBalance()
	if err != nil {
		return nil, err
	}

	// 构建合约账户余额映射（使用 UmWalletBalance，USDT-M 合约）
	balancesMap := make(map[string]*model.Balance)
	for _, bal := range balances {
		umBalance, _ := strconv.ParseFloat(bal.UmWalletBalance, 64)
		// 合约账户的可用余额通常是钱包余额减去未实现盈亏
		umUnrealizedPNL, _ := strconv.ParseFloat(bal.UmUnrealizedPNL, 64)
		available := umBalance - umUnrealizedPNL
		if available < 0 {
			available = 0
		}
		locked := 0.0 // 合约账户通常没有锁定余额的概念
		total := umBalance

		updateTime := time.Unix(bal.UpdateTime/1000, 0)
		if bal.UpdateTime == 0 {
			updateTime = time.Now()
		}

		// 只记录有余额的币种
		if total > 0 || available > 0 {
			balancesMap[bal.Asset] = &model.Balance{
				Asset:      bal.Asset,
				Available:  available,
				Locked:     locked,
				Total:      total,
				UpdateTime: updateTime,
			}
		}
	}

	return balancesMap, nil
}
