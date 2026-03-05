package position

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/analytics"
	"github.com/qw225967/auto-monitor/internal/exchange"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/onchain"
	"github.com/qw225967/auto-monitor/internal/statistics"
	"github.com/qw225967/auto-monitor/internal/trader"
	"github.com/qw225967/auto-monitor/internal/utils/logger"
	"github.com/qw225967/auto-monitor/internal/utils/parallel"

	"go.uber.org/zap"
)

// PositionManager 持仓管理器
// 负责跨交易所/链上的持仓聚合分析和查询
type PositionManager struct {
	mu sync.RWMutex

	// 钱包管理器引用（用于获取最新数据）
	walletManager *WalletManager

	// 跟踪的交易对（symbol集合）
	trackedSymbols map[string]bool

	// Analytics 提供者（symbol -> Analytics 映射）
	analyticsProviders map[string]analytics.Analyzer

	// Onchain Trader 映射（symbol -> OnchainTrader 映射）
	onchainTraders map[string]trader.OnchainTrader

	// 各 symbol 的 trader 类型（供 updateSwapInfoAmountForSymbol 等在没有 trigger 上下文时调用 GetSize）
	traderATypeBySymbol map[string]string
	traderBTypeBySymbol map[string]string

	// Web 配置的固定 size（+A-B / -A+B），用于 swapInfo.Amount 只接受页面设置、不被余额/订单簿覆盖
	configuredSizeABBySymbol map[string]float64
	configuredSizeBABySymbol map[string]float64

	// 价格缓存（symbol -> 最新价格数据）
	latestPrices map[string]*model.PriceData

	// 上下文管理（用于定时任务）
	ctx    context.Context
	cancel context.CancelFunc

	// 协程组（用于管理定时任务）
	routineGroup *parallel.RoutineGroup

	// 日志实例
	logger *zap.SugaredLogger
}

// SymbolPositionSummary 某个 symbol 的持仓汇总信息
type SymbolPositionSummary struct {
	Symbol string `json:"symbol"` // 交易对符号（如 BTCUSDT）

	// 交易所持仓汇总
	TotalExchangeLongSize      float64 `json:"total_exchange_long_size"`      // 所有交易所的多头持仓总数量
	TotalExchangeShortSize     float64 `json:"total_exchange_short_size"`     // 所有交易所的空头持仓总数量
	TotalExchangeLongValue     float64 `json:"total_exchange_long_value"`     // 所有交易所的多头持仓总价值
	TotalExchangeShortValue    float64 `json:"total_exchange_short_value"`    // 所有交易所的空头持仓总价值
	TotalExchangeUnrealizedPnl float64 `json:"total_exchange_unrealized_pnl"` // 所有交易所的未实现盈亏总和

	// 链上余额汇总
	TotalOnchainBalance float64 `json:"total_onchain_balance"` // 所有链上的代币余额总和
	TotalOnchainValue   float64 `json:"total_onchain_value"`   // 所有链上的代币余额总价值

	// 总汇总
	TotalQuantity float64 `json:"total_quantity"` // 总数量（交易所持仓 + 链上余额，Long为正，Short为负）
	TotalValue    float64 `json:"total_value"`    // 总价值（交易所持仓价值 + 链上余额价值）

	// 分布详情
	ExchangePositions []*ExchangePositionDetail `json:"exchange_positions"` // 各交易所的持仓详情
	OnchainBalances   []*OnchainBalanceDetail   `json:"onchain_balances"`   // 各链上的余额详情
}

// ExchangePositionDetail 某个交易所的持仓详情
type ExchangePositionDetail struct {
	ExchangeType string `json:"exchange_type"` // 交易所类型（如 "binance"）

	// 持仓信息
	Side          string  `json:"side"`           // 持仓方向（LONG 或 SHORT）
	Size          float64 `json:"size"`           // 持仓数量（Long为正，Short为负）
	EntryPrice    float64 `json:"entry_price"`    // 开仓均价
	MarkPrice     float64 `json:"mark_price"`     // 标记价格
	UnrealizedPnl float64 `json:"unrealized_pnl"` // 未实现盈亏
	Leverage      int     `json:"leverage"`       // 杠杆倍数
	PositionValue float64 `json:"position_value"` // 持仓价值（Size * MarkPrice）
}

// OnchainBalanceDetail 某个链上的余额详情
type OnchainBalanceDetail struct {
	ClientID   string `json:"client_id"`   // 链上客户端ID
	ChainIndex string `json:"chain_index"` // 链索引（如 "56" 表示 BSC）
	Symbol     string `json:"symbol"`      // 代币符号

	Balance    float64 `json:"balance"`     // 余额数量
	TokenPrice float64 `json:"token_price"` // 代币单价（美元）
	Value      float64 `json:"value"`       // 余额价值（Balance * TokenPrice）
}

// 全局 PositionManager 实例
var globalPositionManager *PositionManager
var globalPositionManagerOnce sync.Once

// InitPositionManager 初始化持仓管理器（会自动初始化 WalletManager）
// traders: Trader 列表（统一交易所和链上）
// onchainClients: 链上客户端配置列表（map[clientID]*OnchainClientConfig）
// refreshInterval: 刷新间隔
func InitPositionManager(traders []trader.Trader, onchainClients map[string]*OnchainClientConfig, refreshInterval time.Duration) *PositionManager {
	var pm *PositionManager
	globalPositionManagerOnce.Do(func() {
		// 初始化 WalletManager（直接使用传入的 traders）
		InitWalletManager(traders, onchainClients, refreshInterval)

		// 获取 WalletManager 实例
		walletManager := GetWalletManager()
		if walletManager == nil {
			logger.GetLoggerInstance().Named("PositionManager").Sugar().Error("WalletManager 初始化失败")
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		pm = &PositionManager{
			walletManager:            walletManager,
			trackedSymbols:           make(map[string]bool),
			analyticsProviders:       make(map[string]analytics.Analyzer),
			onchainTraders:           make(map[string]trader.OnchainTrader),
			traderATypeBySymbol:      make(map[string]string),
			traderBTypeBySymbol:      make(map[string]string),
			configuredSizeABBySymbol: make(map[string]float64),
			configuredSizeBABySymbol: make(map[string]float64),
			latestPrices:             make(map[string]*model.PriceData),
			ctx:                      ctx,
			cancel:                   cancel,
			routineGroup:             parallel.NewRoutineGroup(),
			logger:                   logger.GetLoggerInstance().Named("PositionManager").Sugar(),
		}
		globalPositionManager = pm

		// 启动1s定时任务，更新 swapInfo.Amount
		pm.routineGroup.GoSafe(func() {
			pm.updateSwapInfoAmountLoop()
		})
	})
	return pm
}

// GetPositionManager 获取全局 PositionManager 实例
func GetPositionManager() *PositionManager {
	return globalPositionManager
}

// NewPositionManager 创建新的持仓管理器（需要 WalletManager 已初始化）
func NewPositionManager(walletManager *WalletManager) *PositionManager {
	if walletManager == nil {
		return nil
	}

	return &PositionManager{
		walletManager:            walletManager,
		trackedSymbols:           make(map[string]bool),
		analyticsProviders:       make(map[string]analytics.Analyzer),
		traderATypeBySymbol:      make(map[string]string),
		traderBTypeBySymbol:      make(map[string]string),
		configuredSizeABBySymbol: make(map[string]float64),
		configuredSizeBABySymbol: make(map[string]float64),
		logger:                   logger.GetLoggerInstance().Named("PositionManager").Sugar(),
	}
}

// RegisterSymbol 注册需要跟踪的交易对
// 当 trigger 启动时，应该调用此方法注册 symbol
func (pm *PositionManager) RegisterSymbol(symbol string) {
	if symbol == "" {
		pm.logger.Warn("尝试注册空的 symbol")
		return
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.trackedSymbols == nil {
		pm.trackedSymbols = make(map[string]bool)
	}

	pm.trackedSymbols[symbol] = true
	pm.logger.Infof("已注册交易对: %s", symbol)
}

// RegisterOnchainTrader 注册 Onchain Trader
// 当 trigger 启动时，应该调用此方法注册 Onchain Trader 实例
func (pm *PositionManager) RegisterOnchainTrader(symbol string, onchainTrader trader.OnchainTrader) {
	if symbol == "" {
		pm.logger.Warn("尝试注册空的 symbol 的 onchain trader")
		return
	}

	if onchainTrader == nil {
		pm.logger.Warnf("尝试注册 symbol %s 的 onchain trader 为 nil", symbol)
		return
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.onchainTraders == nil {
		pm.onchainTraders = make(map[string]trader.OnchainTrader)
	}

	pm.onchainTraders[symbol] = onchainTrader
	pm.logger.Infof("已注册 symbol %s 的 onchain trader", symbol)
}

// RegisterOnchainClient 注册 Onchain 客户端（向后兼容，接受 OnchainClient）
// 当 trigger 启动时，应该调用此方法注册 Onchain 客户端实例
func (pm *PositionManager) RegisterOnchainClient(symbol string, client onchain.OnchainClient) {
	if symbol == "" {
		pm.logger.Warn("尝试注册空的 symbol 的 onchain client")
		return
	}

	if client == nil {
		pm.logger.Warnf("尝试注册 symbol %s 的 onchain client 为 nil", symbol)
		return
	}

	// 转换为 OnchainTrader
	onchainTrader := trader.NewOnchainTrader(client, "onchain:56")
	pm.RegisterOnchainTrader(symbol, onchainTrader)
}

// UnregisterOnchainClient 取消注册 Onchain 客户端（向后兼容）
func (pm *PositionManager) UnregisterOnchainClient(symbol string) {
	pm.UnregisterOnchainTrader(symbol)
}

// UnregisterOnchainTrader 取消注册 Onchain Trader
func (pm *PositionManager) UnregisterOnchainTrader(symbol string) {
	if symbol == "" {
		return
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.onchainTraders != nil {
		delete(pm.onchainTraders, symbol)
		pm.logger.Infof("已取消注册 symbol %s 的 onchain trader", symbol)
	}
}

// RegisterTraderTypes 注册 symbol 对应的 traderAType、traderBType，供 updateSwapInfoAmountForSymbol 等调用 GetSize 时使用
func (pm *PositionManager) RegisterTraderTypes(symbol string, traderAType, traderBType string) {
	if symbol == "" {
		return
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.traderATypeBySymbol == nil {
		pm.traderATypeBySymbol = make(map[string]string)
	}
	if pm.traderBTypeBySymbol == nil {
		pm.traderBTypeBySymbol = make(map[string]string)
	}
	pm.traderATypeBySymbol[symbol] = traderAType
	pm.traderBTypeBySymbol[symbol] = traderBType
	pm.logger.Debugf("已注册 symbol %s 的 trader 类型: A=%s, B=%s", symbol, traderAType, traderBType)
}

// UnregisterTraderTypes 取消注册 symbol 的 trader 类型
func (pm *PositionManager) UnregisterTraderTypes(symbol string) {
	if symbol == "" {
		return
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.traderATypeBySymbol != nil {
		delete(pm.traderATypeBySymbol, symbol)
	}
	if pm.traderBTypeBySymbol != nil {
		delete(pm.traderBTypeBySymbol, symbol)
	}
	pm.logger.Debugf("已取消注册 symbol %s 的 trader 类型", symbol)
}

// SetConfiguredSizeForSymbol 将 Web 配置的 size（triggerABSize/triggerBASize）同步到该 symbol 的 Analytics 与本地缓存，
// 使 swapInfo.Amount 只接受页面设置的固定值，不被余额/订单簿覆盖。
func (pm *PositionManager) SetConfiguredSizeForSymbol(symbol string, sizeAB, sizeBA float64) {
	if symbol == "" {
		return
	}
	pm.mu.Lock()
	if pm.configuredSizeABBySymbol == nil {
		pm.configuredSizeABBySymbol = make(map[string]float64)
	}
	if pm.configuredSizeBABySymbol == nil {
		pm.configuredSizeBABySymbol = make(map[string]float64)
	}
	if sizeAB > 0 {
		pm.configuredSizeABBySymbol[symbol] = sizeAB
	}
	if sizeBA > 0 {
		pm.configuredSizeBABySymbol[symbol] = sizeBA
	}
	analyzer := pm.analyticsProviders[symbol]
	pm.mu.Unlock()

	if analyzer != nil {
		if a, ok := analyzer.(*analytics.Analytics); ok {
			if sizeAB > 0 {
				a.SetMaxSize("AB", sizeAB)
				pm.logger.Debugf("SetConfiguredSizeForSymbol: symbol %s AB size = %.6f", symbol, sizeAB)
			}
			if sizeBA > 0 {
				a.SetMaxSize("BA", sizeBA)
				pm.logger.Debugf("SetConfiguredSizeForSymbol: symbol %s BA size = %.6f", symbol, sizeBA)
			}
		}
	}
}

// UpdatePrice 更新指定 symbol 的最新价格（供 Trigger 调用）
func (pm *PositionManager) UpdatePrice(symbol string, priceData *model.PriceData) {
	if symbol == "" || priceData == nil {
		return
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.latestPrices == nil {
		pm.latestPrices = make(map[string]*model.PriceData)
	}

	pm.latestPrices[symbol] = priceData
	pm.logger.Debugf("已更新 symbol %s 的价格缓存: Bid=%.6f, Ask=%.6f", symbol, priceData.BidPrice, priceData.AskPrice)
}

// GetLatestPrice 获取指定 symbol 的最新价格
func (pm *PositionManager) GetLatestPrice(symbol string) *model.PriceData {
	if symbol == "" {
		return nil
	}

	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if pm.latestPrices == nil {
		return nil
	}

	return pm.latestPrices[symbol]
}

// TriggerImmediateUpdate 立即触发指定 symbol 的 swapInfo.Amount 更新
// 用于在 Trigger 启动后立即更新，而不是等待定时任务
func (pm *PositionManager) TriggerImmediateUpdate(symbol string) {
	if symbol == "" {
		return
	}

	pm.logger.Infof("立即触发 symbol %s 的 swapInfo.Amount 更新", symbol)

	// 获取 onchain clients
	pm.walletManager.mu.RLock()
	onchainClients := make(map[string]*OnchainClientConfig)
	for k, v := range pm.walletManager.onchainClients {
		onchainClients[k] = v
	}
	pm.walletManager.mu.RUnlock()

	// 执行更新
	pm.updateSwapInfoAmountForSymbol(symbol, onchainClients)
}

// ForceRefreshBalance 强制刷新钱包余额
// 交易成功后调用此方法，确保后续交易使用最新的余额数据
func (pm *PositionManager) ForceRefreshBalance() error {
	if pm.walletManager == nil {
		return nil
	}
	return pm.walletManager.ForceRefresh()
}

// ForceRefreshAndUpdate 强制刷新余额并更新指定 symbol 的 swapInfo.Amount
// 这是一个组合方法，用于交易成功后确保数据同步
func (pm *PositionManager) ForceRefreshAndUpdate(symbol string) {
	// 1. 先刷新钱包余额
	if err := pm.ForceRefreshBalance(); err != nil {
		pm.logger.Warnf("强制刷新余额失败: %v", err)
		// 即使失败也继续更新 swapInfo.Amount
	}

	// 2. 更新 swapInfo.Amount
	if symbol != "" {
		pm.TriggerImmediateUpdate(symbol)
	}
}

// RegisterAnalytics 注册 Analytics 提供者
// 当 trigger 启动时，应该调用此方法注册 Analytics 实例
func (pm *PositionManager) RegisterAnalytics(symbol string, analyzer analytics.Analyzer) {
	if symbol == "" {
		pm.logger.Warn("尝试注册空的 symbol 的 Analytics")
		return
	}

	if analyzer == nil {
		pm.logger.Warnf("尝试注册 symbol %s 的 Analytics 为 nil", symbol)
		return
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.analyticsProviders == nil {
		pm.analyticsProviders = make(map[string]analytics.Analyzer)
	}

	pm.analyticsProviders[symbol] = analyzer
	pm.logger.Infof("已注册交易对 %s 的 Analytics", symbol)
}

// UnregisterAnalytics 取消注册 Analytics 提供者
func (pm *PositionManager) UnregisterAnalytics(symbol string) {
	if symbol == "" {
		return
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.analyticsProviders != nil {
		delete(pm.analyticsProviders, symbol)
		pm.logger.Infof("已取消注册交易对 %s 的 Analytics", symbol)
	}
}

// UnregisterSymbol 取消注册交易对
func (pm *PositionManager) UnregisterSymbol(symbol string) {
	if symbol == "" {
		return
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.trackedSymbols != nil {
		delete(pm.trackedSymbols, symbol)
		pm.logger.Infof("已取消注册交易对: %s", symbol)
	}
}

// IsSymbolTracked 检查 symbol 是否已注册
func (pm *PositionManager) IsSymbolTracked(symbol string) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if pm.trackedSymbols == nil {
		return false
	}
	return pm.trackedSymbols[symbol]
}

// GetTrackedSymbols 获取所有已注册的交易对
func (pm *PositionManager) GetTrackedSymbols() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if pm.trackedSymbols == nil {
		return nil
	}

	symbols := make([]string, 0, len(pm.trackedSymbols))
	for symbol := range pm.trackedSymbols {
		symbols = append(symbols, symbol)
	}
	return symbols
}

// GetSymbolPositionSummary 获取指定 symbol 的持仓汇总信息
// 聚合所有交易所和链上的持仓/余额数据
func (pm *PositionManager) GetSymbolPositionSummary(symbol string) *SymbolPositionSummary {
	if symbol == "" {
		pm.logger.Warn("Symbol is empty")
		return nil
	}

	// 从 WalletManager 获取最新数据
	walletInfo := pm.walletManager.GetWalletInfo()
	if walletInfo == nil {
		pm.logger.Warn("WalletInfo is nil")
		return nil
	}

	summary := &SymbolPositionSummary{
		Symbol:            symbol,
		ExchangePositions: make([]*ExchangePositionDetail, 0),
		OnchainBalances:   make([]*OnchainBalanceDetail, 0),
	}

	// 从 symbol 中提取基础币种（如 "BTCUSDT" -> "BTC"）
	baseSymbol := extractBaseSymbol(symbol)

	// 1. 聚合所有交易所的持仓
	if walletInfo.ExchangeWallets != nil {
		for exchangeType, exchangeWallet := range walletInfo.ExchangeWallets {
			if exchangeWallet == nil || exchangeWallet.Positions == nil {
				continue
			}

			// 查找该交易所的持仓
			position, exists := exchangeWallet.Positions[symbol]
			if !exists || position == nil {
				continue
			}

			// 计算持仓数量（Long为正，Short为负）
			var size float64
			if position.Side == model.PositionSideLong {
				size = position.Size
				summary.TotalExchangeLongSize += position.Size
				summary.TotalExchangeLongValue += position.Size * position.MarkPrice
			} else if position.Side == model.PositionSideShort {
				size = -position.Size
				summary.TotalExchangeShortSize += position.Size
				summary.TotalExchangeShortValue += position.Size * position.MarkPrice
			}

			summary.TotalExchangeUnrealizedPnl += position.UnrealizedPnl

			// 添加到详情列表
			summary.ExchangePositions = append(summary.ExchangePositions, &ExchangePositionDetail{
				ExchangeType:  exchangeType,
				Side:          string(position.Side),
				Size:          size,
				EntryPrice:    position.EntryPrice,
				MarkPrice:     position.MarkPrice,
				UnrealizedPnl: position.UnrealizedPnl,
				Leverage:      position.Leverage,
				PositionValue: position.Size * position.MarkPrice,
			})
		}
	}

	// 2. 聚合所有链上的余额
	if baseSymbol != "" && walletInfo.OnchainBalances != nil {
		for chainIndex, symbolMap := range walletInfo.OnchainBalances {
			if symbolMap == nil {
				continue
			}

			// 查找匹配的代币
			asset, exists := symbolMap[baseSymbol]
			if !exists {
				continue
			}

			// 解析余额和价格
			balance, err := strconv.ParseFloat(asset.Balance, 64)
			if err != nil || balance <= 0 {
				continue
			}

			var tokenPrice float64
			if asset.TokenPrice != "" {
				tokenPrice, _ = strconv.ParseFloat(asset.TokenPrice, 64)
			}

			value := balance * tokenPrice
			summary.TotalOnchainBalance += balance
			summary.TotalOnchainValue += value

			// 添加到详情列表
			summary.OnchainBalances = append(summary.OnchainBalances, &OnchainBalanceDetail{
				ClientID:   "",
				ChainIndex: chainIndex,
				Balance:    balance,
				TokenPrice: tokenPrice,
				Value:      value,
			})
		}
	}

	// 3. 如果没有找到任何持仓或余额，返回 nil
	if len(summary.ExchangePositions) == 0 && len(summary.OnchainBalances) == 0 {
		return nil
	}

	// 4. 计算总数量和总价值
	// 总数量 = 交易所多头 - 交易所空头 + 链上余额
	summary.TotalQuantity = summary.TotalExchangeLongSize - summary.TotalExchangeShortSize + summary.TotalOnchainBalance
	// 总价值 = 交易所持仓价值 + 链上余额价值
	summary.TotalValue = summary.TotalExchangeLongValue + summary.TotalExchangeShortValue + summary.TotalOnchainValue

	// 5. 过滤：总价值低于 3 USDT 的不展示
	const minValueThreshold = 3.0
	if summary.TotalValue < minValueThreshold {
		return nil
	}

	return summary
}

// GetAllSymbolPositionSummaries 获取所有 symbol 的持仓汇总信息
func (pm *PositionManager) GetAllSymbolPositionSummaries() map[string]*SymbolPositionSummary {
	walletInfo := pm.walletManager.GetWalletInfo()
	if walletInfo == nil {
		pm.logger.Warn("WalletInfo is nil")
		return nil
	}

	result := make(map[string]*SymbolPositionSummary)

	// 收集所有有持仓的 symbol
	symbolSet := make(map[string]bool)

	// 从交易所持仓中收集
	if walletInfo.ExchangeWallets != nil {
		for _, exchangeWallet := range walletInfo.ExchangeWallets {
			if exchangeWallet == nil || exchangeWallet.Positions == nil {
				continue
			}
			for symbol := range exchangeWallet.Positions {
				symbolSet[symbol] = true
			}
		}
	}

	// 从链上余额中收集（需要转换为 symbol 格式，如 BTC -> BTCUSDT）
	if walletInfo.OnchainBalances != nil {
		for _, symbolMap := range walletInfo.OnchainBalances {
			if symbolMap == nil {
				continue
			}
			for baseSymbol := range symbolMap {
				// 尝试构造 symbol（假设是 USDT 计价）
				symbol := baseSymbol + "USDT"
				symbolSet[symbol] = true
			}
		}
	}

	// 为每个 symbol 生成汇总信息
	for symbol := range symbolSet {
		summary := pm.GetSymbolPositionSummary(symbol)
		if summary != nil {
			result[symbol] = summary
		}
	}

	return result
}

// GetSize 获取指定 symbol 的交易 size
// direction: "AB" 表示 +A-B（链上买入，交易所卖出），"BA" 表示 -A+B（链上卖出，交易所买入）
// exchangeType: 交易所类型（如 "binance", "bybit" 等），用于指定查询哪个交易所的余额
// chainIndex: 链索引（如 "56" 表示 BSC），用于指定查询哪条链的余额
// exchangePriceData: 交易所价格数据（用于成本计算）
// onchainPriceData: 链上价格数据（用于成本计算，可以为 nil，将使用交易所价格作为替代）
// traderAType: A 的 trader 类型（如 "onchain:56" 或 "binance:futures"）
// traderBType: B 的 trader 类型（如 "onchain:56" 或 "binance:futures"）
// 每次交易时调用此方法获取 size，会根据 Analytics 的滑点、成本、推荐size等信息进行权衡
func (pm *PositionManager) GetSize(symbol string, direction string, exchangeType string, chainIndex string, exchangePriceData, onchainPriceData *model.PriceData, traderAType, traderBType string) float64 {
	// 1. 参数验证
	if !pm.validateGetSizeParams(symbol, direction, exchangePriceData) {
		return 0
	}

	// 2. 获取 Analytics 实例
	analyzer := pm.getAnalyticsForSymbol(symbol)
	if analyzer == nil {
		return 0
	}

	// 3. 处理价格数据
	priceDataForCost := pm.preparePriceDataForCost(onchainPriceData, exchangePriceData)

	// 4. 获取并限制 maxSize
	maxSize := pm.getAndLimitMaxSize(analyzer, direction, exchangePriceData)
	if maxSize <= 0 {
		return 0
	}

	// 5. 检查余额并获取可用 size
	availableSizeFromBalance := pm.getAvailableSizeFromBalance(symbol, direction, exchangeType, chainIndex, exchangePriceData, onchainPriceData, traderAType, traderBType)
	if availableSizeFromBalance <= 0 {
		// 当无法获取余额（如链上/钱包未就绪）但已有配置的 maxSize 时，使用配置的 size，使搬砖区调整的 size 能生效
		if maxSize > 0 {
			availableSizeFromBalance = maxSize
			pm.logger.Debugf("GetSize: %s %s 可用余额为 0，使用配置 maxSize %.6f", symbol, direction, maxSize)
		} else {
			return 0
		}
	}

	// 6. 确定最终 size（取 maxSize 和余额的较小值）
	finalSize := pm.calculateFinalSize(maxSize, availableSizeFromBalance)
	if finalSize <= 0 {
		return 0
	}

	// 7. 取整：若双方有 quanto_multiplier 则取最大值作为步长，size 须 >= 步长并按步长向下取整；否则按百/十/个向下取整
	stepQuanto := pm.getMaxQuantoFromTraders(symbol, traderAType, traderBType)
	if stepQuanto > 0 {
		if finalSize < stepQuanto {
			pm.logger.Debugf("GetSize: 最终 size %.6f 小于 quanto 步长 %.6f，返回 0", finalSize, stepQuanto)
			return 0
		}
		finalSize = math.Floor(finalSize/stepQuanto) * stepQuanto
	} else {
		finalSize = pm.roundDownToHundred(finalSize)
	}
	if finalSize <= 0 {
		return 0
	}

	// 8. 检查最小交易价值
	if !pm.checkMinTradeValue(finalSize, direction, exchangePriceData) {
		return 0
	}

	// 9. 计算交易价值（用于后续记录）
	tradeValueUSDT := pm.calculateTradeValue(finalSize, direction, exchangePriceData)

	// 10. 检查成本并记录
	if !pm.checkAndRecordCost(symbol, direction, finalSize, exchangePriceData, priceDataForCost, analyzer) {
		return 0
	}

	// 11. 记录 Size 数据
	pm.recordSizeData(symbol, finalSize, tradeValueUSDT)

	pm.logger.Debugf("GetSize 结果 - Symbol: %s, Direction: %s, maxSize: %.6f, 可用余额: %.6f, 最终size: %.6f",
		symbol, direction, maxSize, availableSizeFromBalance, finalSize)

	return finalSize
}

// GetAvailableBalanceSize 仅根据 A/B 余额计算可用交易数量，不依赖 TrackSymbol 或 Analytics
// 供 Trigger 在下单前做余额不足检查。返回 0 表示无法获取或余额不足。
func (pm *PositionManager) GetAvailableBalanceSize(symbol string, direction string, exchangeType string, chainIndex string, exchangePriceData, onchainPriceData *model.PriceData, traderAType, traderBType string) float64 {
	if symbol == "" || (direction != "AB" && direction != "BA") {
		return 0
	}
	if exchangePriceData == nil || (exchangePriceData.BidPrice <= 0 && exchangePriceData.AskPrice <= 0) {
		return 0
	}
	return pm.getAvailableSizeFromBalance(symbol, direction, exchangeType, chainIndex, exchangePriceData, onchainPriceData, traderAType, traderBType)
}

// validateGetSizeParams 验证 GetSize 方法的参数
func (pm *PositionManager) validateGetSizeParams(symbol string, direction string, exchangePriceData *model.PriceData) bool {
	if symbol == "" {
		pm.logger.Warn("GetSize: Symbol is empty, returning 0")
		return false
	}

	if direction != "AB" && direction != "BA" {
		pm.logger.Warnf("GetSize: Invalid direction: %s, returning 0", direction)
		return false
	}

	if !pm.IsSymbolTracked(symbol) {
		pm.logger.Debugf("GetSize: Symbol %s 未注册，返回 0", symbol)
		return false
	}

	if exchangePriceData == nil {
		pm.logger.Warnf("GetSize: 交易所价格数据为空，返回 0")
		return false
	}

	return true
}

// getAnalyticsForSymbol 获取指定 symbol 的 Analytics 实例
func (pm *PositionManager) getAnalyticsForSymbol(symbol string) analytics.Analyzer {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	analyzer, hasAnalytics := pm.analyticsProviders[symbol]
	if !hasAnalytics || analyzer == nil {
		pm.logger.Debugf("GetSize: Symbol %s 没有 Analytics 提供者，返回 0", symbol)
		return nil
	}

	return analyzer
}

// preparePriceDataForCost 准备用于成本计算的价格数据
func (pm *PositionManager) preparePriceDataForCost(onchainPriceData, exchangePriceData *model.PriceData) *model.PriceData {
	if onchainPriceData != nil {
		return onchainPriceData
	}
	pm.logger.Debugf("GetSize: 链上价格数据不可用，使用交易所价格作为替代")
	return exchangePriceData
}

// getAndLimitMaxSize 获取并限制 maxSize
func (pm *PositionManager) getAndLimitMaxSize(analyzer analytics.Analyzer, direction string, exchangePriceData *model.PriceData) float64 {
	maxSize := analyzer.GetMaxSize(direction)
	if maxSize <= 0 {
		pm.logger.Debugf("GetSize: Analytics 返回无效的 maxSize: %.6f，返回 0", maxSize)
		return 0
	}

	// 根据价格计算最大交易 size（确保交易价值不超过限制）
	const maxTradeValueUSDT = 2000.0 // 最大单次交易价值（USDT）
	var maxTradeSize float64
	if direction == "BA" {
		// -A+B: 链上卖出，交易所买入，使用买入价格（AskPrice）
		if exchangePriceData.AskPrice > 0 {
			maxTradeSize = maxTradeValueUSDT / exchangePriceData.AskPrice
		} else {
			pm.logger.Warnf("GetSize: 买入价格无效，无法计算最大交易 size")
			return 0
		}
	} else {
		// +A-B: 链上买入，交易所卖出，使用卖出价格（BidPrice）来估算价值
		if exchangePriceData.BidPrice > 0 {
			maxTradeSize = maxTradeValueUSDT / exchangePriceData.BidPrice
		} else {
			pm.logger.Warnf("GetSize: 卖出价格无效，无法计算最大交易 size")
			return 0
		}
	}

	// 如果 maxSize 超过计算出的最大交易 size，则限制
	if maxSize > maxTradeSize {
		pm.logger.Debugf("GetSize: maxSize %.6f 超过最大交易限制 %.6f（价值限制: %.2f USDT），限制为 %.6f",
			maxSize, maxTradeSize, maxTradeValueUSDT, maxTradeSize)
		maxSize = maxTradeSize
	}

	return maxSize
}

// parseTraderType 解析 trader 类型字符串
// 格式: "type:value" (如 "binance:futures" 或 "onchain:56")
// 返回: isOnchain, exchangeType, marketType, chainIndex, error
func parseTraderType(traderType string) (isOnchain bool, exchangeType, marketType, chainIndex string, err error) {
	if traderType == "" {
		return false, "", "", "", fmt.Errorf("trader type is empty")
	}

	parts := strings.Split(traderType, ":")
	if len(parts) != 2 {
		return false, "", "", "", fmt.Errorf("invalid trader type format: %s", traderType)
	}

	typeStr := parts[0]
	value := parts[1]

	if typeStr == "onchain" {
		return true, "", "", value, nil
	}

	// 交易所类型（如 binance, gate, bybit, bitget 等）
	return false, typeStr, value, "", nil // value 是 marketType（spot 或 futures）
}

// getAvailableSizeFromBalance 根据方向检查余额并获取可用 size
func (pm *PositionManager) getAvailableSizeFromBalance(symbol string, direction string, exchangeType string, chainIndex string, exchangePriceData, onchainPriceData *model.PriceData, traderAType, traderBType string) float64 {
	// 从 symbol 中提取基础币种（如 BTCUSDT -> BTC）
	baseSymbol := extractBaseSymbol(symbol)
	if baseSymbol == "" {
		pm.logger.Warnf("GetSize: 无法从 symbol %s 提取基础币种，返回 0", symbol)
		return 0
	}

	walletInfo := pm.walletManager.GetWalletInfo()
	if walletInfo == nil {
		pm.logger.Warnf("GetSize: WalletInfo 为空，无法检查余额")
		return 0
	}

	// 解析 trader 类型
	aIsOnchain, aExchangeType, aMarketType, aChainIndex, err := parseTraderType(traderAType)
	if err != nil {
		pm.logger.Warnf("GetSize: 解析 traderAType %s 失败: %v，使用默认逻辑", traderAType, err)
		// 向后兼容：使用默认逻辑
		return pm.getAvailableSizeFromBalanceLegacy(symbol, direction, exchangeType, chainIndex, exchangePriceData, onchainPriceData)
	}

	bIsOnchain, bExchangeType, bMarketType, bChainIndex, err := parseTraderType(traderBType)
	if err != nil {
		pm.logger.Warnf("GetSize: 解析 traderBType %s 失败: %v，使用默认逻辑", traderBType, err)
		// 向后兼容：使用默认逻辑
		return pm.getAvailableSizeFromBalanceLegacy(symbol, direction, exchangeType, chainIndex, exchangePriceData, onchainPriceData)
	}

	// 确定 A 和 B 的方向和交易类型
	var sideA, sideB model.OrderSide
	var marketTypeA, marketTypeB model.MarketType
	var chainIdxA, chainIdxB string
	var exchangeTypeA, exchangeTypeB string

	if direction == "AB" {
		// +A-B: A 买入，B 卖出
		sideA = model.OrderSideBuy
		sideB = model.OrderSideSell
	} else {
		// -A+B: A 卖出，B 买入
		sideA = model.OrderSideSell
		sideB = model.OrderSideBuy
	}

	// 设置 A 的信息
	if aIsOnchain {
		chainIdxA = aChainIndex
		if chainIdxA == "" {
			chainIdxA = chainIndex // 使用传入的 chainIndex
		}
		marketTypeA = "" // 链上没有 marketType
	} else {
		exchangeTypeA = aExchangeType
		if aMarketType == "spot" {
			marketTypeA = model.MarketTypeSpot
		} else {
			marketTypeA = model.MarketTypeFutures
		}
	}

	// 设置 B 的信息
	if bIsOnchain {
		chainIdxB = bChainIndex
		if chainIdxB == "" {
			chainIdxB = chainIndex // 使用传入的 chainIndex
		}
		marketTypeB = "" // 链上没有 marketType
	} else {
		exchangeTypeB = bExchangeType
		if bMarketType == "spot" {
			marketTypeB = model.MarketTypeSpot
		} else {
			marketTypeB = model.MarketTypeFutures
		}
	}

	// 分别获取 A 和 B 的可用 size
	var priceDataA, priceDataB *model.PriceData
	if aIsOnchain {
		priceDataA = onchainPriceData
		if priceDataA == nil {
			priceDataA = exchangePriceData
		}
	} else {
		priceDataA = exchangePriceData
	}

	if bIsOnchain {
		priceDataB = onchainPriceData
		if priceDataB == nil {
			priceDataB = exchangePriceData
		}
	} else {
		priceDataB = exchangePriceData
	}

	availableSizeA := pm.getAvailableSizeForTrader(aIsOnchain, sideA, marketTypeA, symbol, baseSymbol, walletInfo, exchangeTypeA, chainIdxA, priceDataA)
	availableSizeB := pm.getAvailableSizeForTrader(bIsOnchain, sideB, marketTypeB, symbol, baseSymbol, walletInfo, exchangeTypeB, chainIdxB, priceDataB)

	// 返回两者的较小值
	var finalSize float64
	if availableSizeA <= 0 || availableSizeB <= 0 {
		finalSize = 0
	} else if availableSizeA < availableSizeB {
		finalSize = availableSizeA
		pm.logger.Debugf("GetSize: %s 方向 - 可用size: %.6f (受 A 限制: %.6f vs B: %.6f)", direction, finalSize, availableSizeA, availableSizeB)
	} else {
		finalSize = availableSizeB
		pm.logger.Debugf("GetSize: %s 方向 - 可用size: %.6f (受 B 限制: %.6f vs A: %.6f)", direction, finalSize, availableSizeB, availableSizeA)
	}

	// 保留10%余额，只使用90%
	if finalSize > 0 {
		finalSize = finalSize * 0.9
		pm.logger.Debugf("GetSize: %s 方向 - 应用余额保留限制(保留10%%)后的size: %.6f", direction, finalSize)
	}

	return finalSize
}

// getAvailableSizeForTrader 根据 trader 类型、方向、市场类型检查余额并获取可用 size
func (pm *PositionManager) getAvailableSizeForTrader(isOnchain bool, side model.OrderSide, marketType model.MarketType, symbol string, baseSymbol string, walletInfo *model.WalletDetailInfo, exchangeType string, chainIndex string, priceData *model.PriceData) float64 {
	if isOnchain {
		// 链上交易
		return pm.getOnchainAvailableSize(side, baseSymbol, chainIndex, walletInfo, priceData)
	}

	// 交易所交易
	if marketType == model.MarketTypeSpot {
		// 现货交易
		return pm.getExchangeSpotAvailableSize(side, baseSymbol, exchangeType, walletInfo, priceData)
	}

	// 合约交易
	return pm.getExchangeFuturesAvailableSize(side, symbol, baseSymbol, exchangeType, walletInfo, priceData)
}

// getOnchainAvailableSize 链上交易余额检查
// side: 交易方向（Buy=买入需要USDT，Sell=卖出需要coin）
func (pm *PositionManager) getOnchainAvailableSize(side model.OrderSide, baseSymbol string, chainIndex string, walletInfo *model.WalletDetailInfo, priceData *model.PriceData) float64 {
	if walletInfo.OnchainBalances == nil {
		pm.logger.Debugf("GetSize: 链上 - 无链上余额信息，返回 0")
		return 0
	}

	symbolMap, exists := walletInfo.OnchainBalances[chainIndex]
	if !exists || symbolMap == nil {
		pm.logger.Debugf("GetSize: 链上 - 链 %s 无余额信息，返回 0", chainIndex)
		return 0
	}

	if side == model.OrderSideBuy {
		// 买入：检查 USDT 余额
		asset, exists := symbolMap["USDT"]
		if !exists {
			pm.logger.Debugf("GetSize: 链上 - 链 %s 无 USDT 余额，返回 0", chainIndex)
			return 0
		}

		usdtBalance, err := strconv.ParseFloat(asset.Balance, 64)
		if err != nil || usdtBalance <= 0 {
			pm.logger.Debugf("GetSize: 链上 - 链 %s USDT 余额无效: %.2f，返回 0", chainIndex, usdtBalance)
			return 0
		}

		// 使用价格计算可买入的币数量
		var buyPrice float64
		if priceData != nil && priceData.AskPrice > 0 {
			buyPrice = priceData.AskPrice
		} else {
			pm.logger.Debugf("GetSize: 链上 - 买入价格无效，返回 0")
			return 0
		}

		availableSize := usdtBalance / buyPrice
		pm.logger.Debugf("GetSize: 链上买入 - 链 %s USDT余额: %.2f, 买入价格: %.6f, 可买入size: %.6f",
			chainIndex, usdtBalance, buyPrice, availableSize)
		return availableSize
	} else {
		// 卖出：检查对应的 coin 余额
		asset, exists := symbolMap[baseSymbol]
		if !exists {
			pm.logger.Debugf("GetSize: 链上 - 链 %s 无 %s 余额，返回 0", chainIndex, baseSymbol)
			return 0
		}

		coinBalance, err := strconv.ParseFloat(asset.Balance, 64)
		if err != nil || coinBalance <= 0 {
			pm.logger.Debugf("GetSize: 链上 - 链 %s %s 余额无效: %.6f，返回 0", chainIndex, baseSymbol, coinBalance)
			return 0
		}

		pm.logger.Debugf("GetSize: 链上卖出 - 链 %s %s 余额: %.6f, 可卖出size: %.6f",
			chainIndex, baseSymbol, coinBalance, coinBalance)
		return coinBalance
	}
}

// getExchangeSpotAvailableSize 交易所现货余额检查
// side: 交易方向（Buy=买入需要USDT，Sell=卖出需要coin）
func (pm *PositionManager) getExchangeSpotAvailableSize(side model.OrderSide, baseSymbol string, exchangeType string, walletInfo *model.WalletDetailInfo, priceData *model.PriceData) float64 {
	if walletInfo.ExchangeWallets == nil {
		pm.logger.Debugf("GetSize: 交易所现货 - 无交易所钱包信息，返回 0")
		return 0
	}

	exchangeWallet, exists := walletInfo.ExchangeWallets[exchangeType]
	if !exists || exchangeWallet == nil {
		pm.logger.Debugf("GetSize: 交易所现货 - 交易所 %s 的钱包信息不存在，返回 0", exchangeType)
		return 0
	}

	// 使用现货余额
	var balances map[string]*model.Balance
	if exchangeWallet.SpotBalances != nil {
		balances = exchangeWallet.SpotBalances
	} else if exchangeWallet.AccountBalances != nil {
		// 向后兼容：如果没有 SpotBalances，使用 AccountBalances
		balances = exchangeWallet.AccountBalances
	}

	if balances == nil {
		pm.logger.Debugf("GetSize: 交易所现货 - 交易所 %s 无账户余额信息，返回 0", exchangeType)
		return 0
	}

	if side == model.OrderSideBuy {
		// 买入：检查 USDT 余额
		usdtBalance, exists := balances["USDT"]
		if !exists || usdtBalance == nil || usdtBalance.Available <= 0 {
			pm.logger.Debugf("GetSize: 交易所现货 - 交易所 %s 无 USDT 余额，返回 0", exchangeType)
			return 0
		}

		// 使用价格计算可买入的币数量
		if priceData == nil || priceData.AskPrice <= 0 {
			pm.logger.Debugf("GetSize: 交易所现货 - 买入价格无效，返回 0")
			return 0
		}

		availableSize := usdtBalance.Available / priceData.AskPrice
		pm.logger.Debugf("GetSize: 交易所现货买入 - 交易所 %s USDT余额: %.2f, 买入价格: %.6f, 可买入size: %.6f",
			exchangeType, usdtBalance.Available, priceData.AskPrice, availableSize)
		return availableSize
	} else {
		// 卖出：检查对应的 coin 余额
		coinBalance, exists := balances[baseSymbol]
		if !exists || coinBalance == nil || coinBalance.Available <= 0 {
			pm.logger.Debugf("GetSize: 交易所现货 - 交易所 %s 无 %s 余额，返回 0", exchangeType, baseSymbol)
			return 0
		}

		pm.logger.Debugf("GetSize: 交易所现货卖出 - 交易所 %s %s 余额: %.6f, 可卖出size: %.6f",
			exchangeType, baseSymbol, coinBalance.Available, coinBalance.Available)
		return coinBalance.Available
	}
}

// getExchangeFuturesAvailableSize 交易所合约余额检查
// side: 交易方向（Buy=开多需要USDT，Sell=开空需要USDT）
// 需要检查是否有持仓，如果有持仓需要考虑是加仓还是反向开仓
func (pm *PositionManager) getExchangeFuturesAvailableSize(side model.OrderSide, symbol string, baseSymbol string, exchangeType string, walletInfo *model.WalletDetailInfo, priceData *model.PriceData) float64 {
	if walletInfo.ExchangeWallets == nil {
		pm.logger.Debugf("GetSize: 交易所合约 - 无交易所钱包信息，返回 0")
		return 0
	}

	exchangeWallet, exists := walletInfo.ExchangeWallets[exchangeType]
	if !exists || exchangeWallet == nil {
		pm.logger.Debugf("GetSize: 交易所合约 - 交易所 %s 的钱包信息不存在，返回 0", exchangeType)
		return 0
	}

	// 使用合约余额
	var balances map[string]*model.Balance
	if exchangeWallet.FuturesBalances != nil {
		balances = exchangeWallet.FuturesBalances
	} else if exchangeWallet.AccountBalances != nil {
		// 向后兼容：如果没有 FuturesBalances，使用 AccountBalances
		balances = exchangeWallet.AccountBalances
	}

	if balances == nil {
		pm.logger.Debugf("GetSize: 交易所合约 - 交易所 %s 无账户余额信息，返回 0", exchangeType)
		return 0
	}

	// 检查是否有持仓；杠杆：有持仓且 position.Leverage>0 用持仓杠杆，否则用默认合约杠杆
	leverage := constants.DefaultContractLeverage
	var hasPosition bool
	var positionSide model.PositionSide
	var positionSize float64

	if exchangeWallet.Positions != nil {
		if pos, exists := exchangeWallet.Positions[symbol]; exists && pos != nil && pos.Size > 0 {
			hasPosition = true
			positionSide = pos.Side
			positionSize = pos.Size
			if pos.Leverage > 0 {
				leverage = pos.Leverage
			}
		}
	}

	// 获取 USDT 余额
	var usdtBalance float64
	if usdtBal, exists := balances["USDT"]; exists && usdtBal != nil {
		usdtBalance = usdtBal.Available
	}

	if usdtBalance <= 0 {
		pm.logger.Debugf("GetSize: 交易所合约 - 交易所 %s 无 USDT 余额，返回 0", exchangeType)
		return 0
	}

	// 确定价格（买入用 AskPrice，卖出用 BidPrice）
	var tradePrice float64
	if side == model.OrderSideBuy {
		if priceData == nil || priceData.AskPrice <= 0 {
			pm.logger.Debugf("GetSize: 交易所合约 - 买入价格无效，返回 0")
			return 0
		}
		tradePrice = priceData.AskPrice
	} else {
		if priceData == nil || priceData.BidPrice <= 0 {
			pm.logger.Debugf("GetSize: 交易所合约 - 卖出价格无效，返回 0")
			return 0
		}
		tradePrice = priceData.BidPrice
	}

	// 计算【所有】持仓占用的保证金（用于开仓/加仓时扣除），避免多合约时只扣当前 symbol 导致超额开仓、无法维持 1:1 保证金
	// 单笔占用 = |Size| * MarkPrice / Leverage
	var marginUsed float64
	if exchangeWallet.Positions != nil {
		for _, pos := range exchangeWallet.Positions {
			if pos == nil || math.Abs(pos.Size) < 1e-12 {
				continue
			}
			mark := pos.MarkPrice
			if mark <= 0 {
				continue
			}
			lev := pos.Leverage
			if lev <= 0 {
				lev = constants.DefaultContractLeverage
			}
			marginUsed += math.Abs(pos.Size) * mark / float64(lev)
		}
		if marginUsed > 0 {
			pm.logger.Debugf("GetSize: 交易所合约 - 交易所 %s 所有持仓占用保证金: %.2f USDT (共 %d 个持仓)", 
				exchangeType, marginUsed, len(exchangeWallet.Positions))
		}
	}

	// 可用 USDT = 账户余额 - 所有持仓占用保证金（开仓不会直接减少余额，而是占用保证金）
	availableUsdt := usdtBalance - marginUsed
	if availableUsdt <= 0 {
		pm.logger.Debugf("GetSize: 交易所合约 - 交易所 %s 可用USDT不足 (余额: %.2f, 占用保证金: %.2f)，返回 0", 
			exchangeType, usdtBalance, marginUsed)
		return 0
	}

	// 判断操作类型
	var operationType string
	var availableSize float64

	// 保证金可开 size = availableUsdt * leverage / tradePrice
	openSizeFromMargin := availableUsdt * float64(leverage) / tradePrice

	if hasPosition {
		// 有持仓
		if side == model.OrderSideBuy && positionSide == model.PositionSideLong {
			// 买入 + 持多 = 加仓做多
			operationType = "加仓做多"
			availableSize = openSizeFromMargin
		} else if side == model.OrderSideSell && positionSide == model.PositionSideShort {
			// 卖出 + 持空 = 加仓做空
			operationType = "加仓做空"
			availableSize = openSizeFromMargin
		} else if side == model.OrderSideBuy && positionSide == model.PositionSideShort {
			// 买入 + 持空 = 平空仓，或 平空仓+反向开多（期期翻转；单向持仓也可）
			// 平掉 positionSize 后，用 availableUsdt 继续开多
			operationType = "平空仓或平空+开多"
			availableSize = positionSize + openSizeFromMargin
		} else if side == model.OrderSideSell && positionSide == model.PositionSideLong {
			// 卖出 + 持多 = 平多仓，或 平多仓+反向开空（期期翻转；单向持仓也可）
			operationType = "平多仓或平多+开空"
			availableSize = positionSize + openSizeFromMargin
		} else {
			// 未知情况
			pm.logger.Debugf("GetSize: 交易所合约 - 未知的持仓操作组合，返回 0")
			return 0
		}
	} else {
		// 无持仓：开仓（单向持仓时一侧走此分支，翻转同样支持）
		if side == model.OrderSideBuy {
			operationType = "开多仓"
		} else {
			operationType = "开空仓"
		}
		availableSize = openSizeFromMargin
	}

	pm.logger.Debugf("GetSize: 交易所合约 - 交易所 %s %s, USDT余额: %.2f, 占用保证金: %.2f, 可用USDT: %.2f, 价格: %.6f, 可用size: %.6f",
		exchangeType, operationType, usdtBalance, marginUsed, availableUsdt, tradePrice, availableSize)
	return availableSize
}

// getAvailableSizeFromBalanceLegacy 向后兼容的余额检查方法（当 traderType 解析失败时使用）
func (pm *PositionManager) getAvailableSizeFromBalanceLegacy(symbol string, direction string, exchangeType string, chainIndex string, exchangePriceData, onchainPriceData *model.PriceData) float64 {
	baseSymbol := extractBaseSymbol(symbol)
	if baseSymbol == "" {
		pm.logger.Warnf("GetSize: 无法从 symbol %s 提取基础币种，返回 0", symbol)
		return 0
	}

	walletInfo := pm.walletManager.GetWalletInfo()
	if walletInfo == nil {
		pm.logger.Warnf("GetSize: WalletInfo 为空，无法检查余额")
		return 0
	}

	if direction == "AB" {
		return pm.getAvailableSizeForAB(baseSymbol, exchangeType, chainIndex, walletInfo, exchangePriceData, onchainPriceData)
	} else {
		return pm.getAvailableSizeForBA(baseSymbol, exchangeType, chainIndex, walletInfo, exchangePriceData)
	}
}

// getAvailableSizeForAB 获取 AB 方向的可用 size
// AB 方向：+A-B（链上买入，交易所卖出）
// 1. +A 方向：检查指定链上的 USDT 余额是否足够支付 size（链上买入）
// 2. -B 方向：检查指定交易所是否有足够的币余额（现货）或 USDT 余额（合约 1:1 杠杆）
func (pm *PositionManager) getAvailableSizeForAB(baseSymbol string, exchangeType string, chainIndex string, walletInfo *model.WalletDetailInfo, exchangePriceData, onchainPriceData *model.PriceData) float64 {
	if walletInfo.ExchangeWallets == nil {
		pm.logger.Debugf("GetSize: AB 方向 - 无交易所钱包信息，返回 0")
		return 0
	}

	// 获取指定交易所的钱包信息
	exchangeWallet, exists := walletInfo.ExchangeWallets[exchangeType]
	if !exists || exchangeWallet == nil {
		pm.logger.Debugf("GetSize: AB 方向 - 交易所 %s 的钱包信息不存在，返回 0", exchangeType)
		return 0
	}

	// 1. +A 方向：检查指定链上的 USDT 余额是否足够支付链上买入
	// 1.1 获取指定链上的 USDT 余额
	var totalOnchainUSDTBalance float64
	if walletInfo.OnchainBalances != nil {
		if symbolMap, exists := walletInfo.OnchainBalances[chainIndex]; exists && symbolMap != nil {
			if asset, exists := symbolMap["USDT"]; exists {
				balance, err := strconv.ParseFloat(asset.Balance, 64)
				if err == nil && balance > 0 {
					totalOnchainUSDTBalance = balance
				}
			}
		}
	}

	// 1.2 使用链上价格或交易所价格来计算可买入的币数量
	var buyPrice float64
	if onchainPriceData != nil && onchainPriceData.AskPrice > 0 {
		buyPrice = onchainPriceData.AskPrice
	} else if exchangePriceData != nil && exchangePriceData.AskPrice > 0 {
		buyPrice = exchangePriceData.AskPrice
	} else {
		pm.logger.Debugf("GetSize: AB 方向 - 买入价格无效，返回 0")
		return 0
	}

	// 1.3 可买入的币数量 = 链上USDT余额 / 买入价格
	availableSizeFromUSDT := totalOnchainUSDTBalance / buyPrice
	pm.logger.Debugf("GetSize: AB 方向 - +A 方向：链 %s 上USDT余额: %.2f, 买入价格: %.6f, 可买入size: %.6f",
		chainIndex, totalOnchainUSDTBalance, buyPrice, availableSizeFromUSDT)

	// 2. -B 方向：检查指定交易所的 USDT 余额（合约 1:1 杠杆）
	// 2.1 使用合约余额
	var availableSizeForSell float64
	// 1:1 杠杆意味着 USDT 余额 >= size * price
	// 即：size <= USDT余额 / price
	var exchangeUSDTBalance float64
	var futuresBalances map[string]*model.Balance
	if exchangeWallet.FuturesBalances != nil {
		futuresBalances = exchangeWallet.FuturesBalances
	} else if exchangeWallet.AccountBalances != nil {
		// 向后兼容：如果没有 FuturesBalances，使用 AccountBalances
		futuresBalances = exchangeWallet.AccountBalances
	}
	if futuresBalances != nil {
		if usdtBalance, exists := futuresBalances["USDT"]; exists && usdtBalance != nil {
			exchangeUSDTBalance = usdtBalance.Available
		}
	}

	// 2.2 计算可开仓 size
	if exchangeUSDTBalance > 0 && exchangePriceData != nil && exchangePriceData.BidPrice > 0 {
		// 使用卖出价格计算
		availableSizeForSell = exchangeUSDTBalance / exchangePriceData.BidPrice
		pm.logger.Debugf("GetSize: AB 方向 - -B 方向：交易所 %s USDT余额: %.2f, 卖出价格: %.6f, 可开仓size: %.6f",
			exchangeType, exchangeUSDTBalance, exchangePriceData.BidPrice, availableSizeForSell)
	} else {
		pm.logger.Debugf("GetSize: AB 方向 - -B 方向：交易所 %s USDT余额不足或卖出价格无效，返回 0", exchangeType)
		return 0
	}

	// 3.取两个方向的较小值
	var finalSize float64
	if availableSizeFromUSDT < availableSizeForSell {
		finalSize = availableSizeFromUSDT
		pm.logger.Debugf("GetSize: AB 方向 - 可用size: %.6f (受 +A 方向限制)", finalSize)
	} else {
		finalSize = availableSizeForSell
		pm.logger.Debugf("GetSize: AB 方向 - 可用size: %.6f (受 -B 方向限制)", finalSize)
	}

	// 保留10%余额，只使用90%
	finalSize = finalSize * 0.9
	pm.logger.Debugf("GetSize: AB 方向 - 应用余额保留限制(保留10%%)后的size: %.6f", finalSize)
	return finalSize
}

// getAvailableSizeForBA 获取 BA 方向的可用 size
// BA 方向：-A+B（链上卖出，交易所买入）
// 1. -A 方向：检查指定链上是否有足够的币余额（现货）或 USDT 余额（合约 1:1 杠杆）
// 2. +B 方向：检查指定交易所的 USDT 余额是否足够支付 size（交易所买入）
func (pm *PositionManager) getAvailableSizeForBA(baseSymbol string, exchangeType string, chainIndex string, walletInfo *model.WalletDetailInfo, exchangePriceData *model.PriceData) float64 {
	// 1. -A 方向：检查指定链上的余额
	var availableSizeForOnchainSell float64

	// 1.1 先检查链上是否有现货余额（优先使用，确保持仓平衡）
	if walletInfo.OnchainBalances != nil {
		if symbolMap, exists := walletInfo.OnchainBalances[chainIndex]; exists && symbolMap != nil {
			if asset, exists := symbolMap[baseSymbol]; exists {
				balance, err := strconv.ParseFloat(asset.Balance, 64)
				if err == nil && balance > 0 {
					// 有链上余额，使用链上余额
					availableSizeForOnchainSell = balance
					pm.logger.Debugf("GetSize: BA 方向 - -A 方向：链 %s 上有 %s 余额: %.6f", chainIndex, baseSymbol, balance)
				}
			}
		}
	}

	// 1.2 如果没有链上余额，可能是合约交易，检查指定链上的 USDT 余额是否支持 1:1 杠杆开仓
	if availableSizeForOnchainSell <= 0 {
		var totalOnchainUSDTBalance float64
		if walletInfo.OnchainBalances != nil {
			if symbolMap, exists := walletInfo.OnchainBalances[chainIndex]; exists && symbolMap != nil {
				if asset, exists := symbolMap["USDT"]; exists {
					balance, err := strconv.ParseFloat(asset.Balance, 64)
					if err == nil && balance > 0 {
						totalOnchainUSDTBalance = balance
					}
				}
			}
		}

		if totalOnchainUSDTBalance > 0 && exchangePriceData != nil && exchangePriceData.BidPrice > 0 {
			// 使用卖出价格计算（链上卖出）
			availableSizeForOnchainSell = totalOnchainUSDTBalance / exchangePriceData.BidPrice
			pm.logger.Debugf("GetSize: BA 方向 - -A 方向：链 %s 上无币余额，使用合约 1:1 杠杆，USDT余额: %.2f, 卖出价格: %.6f, 可开仓size: %.6f",
				chainIndex, totalOnchainUSDTBalance, exchangePriceData.BidPrice, availableSizeForOnchainSell)
		} else {
			pm.logger.Debugf("GetSize: BA 方向 - -A 方向：链 %s 上无币余额且USDT余额不足或价格无效，返回 0", chainIndex)
			return 0
		}
	}

	// 2. +B 方向：检查指定交易所的余额（平空仓需要币，开多仓需要 USDT）
	if walletInfo.ExchangeWallets == nil {
		pm.logger.Debugf("GetSize: BA 方向 - 无交易所钱包信息，返回 0")
		return 0
	}

	// 获取指定交易所的钱包信息
	exchangeWallet, exists := walletInfo.ExchangeWallets[exchangeType]
	if !exists || exchangeWallet == nil {
		pm.logger.Debugf("GetSize: BA 方向 - 交易所 %s 的钱包信息不存在，返回 0", exchangeType)
		return 0
	}

	// 2.1 检查是否有空仓需要平仓
	var availableSizeForExchangeBuy float64
	var hasShortPosition bool
	var shortPositionSize float64

	if exchangeWallet.Positions != nil {
		if position, exists := exchangeWallet.Positions[baseSymbol+"USDT"]; exists && position != nil {
			if position.Side == "SHORT" && position.Size > 0 {
				hasShortPosition = true
				shortPositionSize = position.Size
			}
		}
	}

	if hasShortPosition {
		// 2.2 有空仓，可以直接平仓（平空仓是买入操作，不需要预先持有币）
		// 可用数量就是空头持仓的大小
		availableSizeForExchangeBuy = shortPositionSize

		pm.logger.Debugf("GetSize: BA 方向 - +B 方向：交易所 %s 有空仓 %.6f，可平仓size: %.6f",
			exchangeType, shortPositionSize, availableSizeForExchangeBuy)
	} else {
		// 2.3 无空仓，检查 USDT 余额用于开多仓（使用合约余额）
		var exchangeUSDTBalance float64
		var futuresBalances map[string]*model.Balance
		if exchangeWallet.FuturesBalances != nil {
			futuresBalances = exchangeWallet.FuturesBalances
		} else if exchangeWallet.AccountBalances != nil {
			// 向后兼容：如果没有 FuturesBalances，使用 AccountBalances
			futuresBalances = exchangeWallet.AccountBalances
		}
		if futuresBalances != nil {
			if usdtBalance, exists := futuresBalances["USDT"]; exists && usdtBalance != nil {
				exchangeUSDTBalance = usdtBalance.Available
			}
		}

		if exchangeUSDTBalance <= 0 {
			pm.logger.Debugf("GetSize: BA 方向 - +B 方向：交易所 %s 无空仓且无 USDT 余额，返回 0", exchangeType)
			return 0
		}

		if exchangePriceData == nil || exchangePriceData.AskPrice <= 0 {
			pm.logger.Debugf("GetSize: BA 方向 - +B 方向：买入价格无效，返回 0")
			return 0
		}

		// 可开多仓数量 = 交易所USDT余额 / 买入价格
		availableSizeForExchangeBuy = exchangeUSDTBalance / exchangePriceData.AskPrice
		pm.logger.Debugf("GetSize: BA 方向 - +B 方向：交易所 %s 无空仓，USDT余额: %.2f, 买入价格: %.6f, 可开多仓size: %.6f",
			exchangeType, exchangeUSDTBalance, exchangePriceData.AskPrice, availableSizeForExchangeBuy)
	}

	// 取两个方向的较小值
	var finalSize float64
	if availableSizeForOnchainSell < availableSizeForExchangeBuy {
		finalSize = availableSizeForOnchainSell
		pm.logger.Debugf("GetSize: BA 方向 - 可用size: %.6f (受 -A 方向限制)", finalSize)
	} else {
		finalSize = availableSizeForExchangeBuy
		pm.logger.Debugf("GetSize: BA 方向 - 可用size: %.6f (受 +B 方向限制)", finalSize)
	}

	// 保留10%余额，只使用90%
	finalSize = finalSize * 0.9
	pm.logger.Debugf("GetSize: BA 方向 - 应用余额保留限制(保留10%%)后的size: %.6f", finalSize)
	return finalSize
}

// calculateFinalSize 计算最终 size（取 maxSize 和余额的较小值）
func (pm *PositionManager) calculateFinalSize(maxSize, availableSizeFromBalance float64) float64 {
	if availableSizeFromBalance <= 0 {
		pm.logger.Debugf("GetSize: 可用余额为 0，返回 0")
		return 0
	}

	if maxSize > availableSizeFromBalance {
		pm.logger.Debugf("GetSize: 余额不足 - maxSize: %.6f > 可用余额: %.6f，使用余额计算", maxSize, availableSizeFromBalance)
		return availableSizeFromBalance
	}

	return maxSize
}

// getMaxQuantoFromTraders 从 traderAType、traderBType 对应的交易所合约中取 quanto_multiplier，返回两者最大值；非合约或未提供则不计入
func (pm *PositionManager) getMaxQuantoFromTraders(symbol, traderAType, traderBType string) float64 {
	var maxQ float64
	for _, tt := range []string{traderAType, traderBType} {
		isOnchain, exType, marketType, _, err := parseTraderType(tt)
		if err != nil || isOnchain || marketType != "futures" {
			continue
		}
		wm := pm.walletManager
		if wm == nil {
			continue
		}
		ex := wm.GetExchange(exType)
		if ex == nil {
			continue
		}
		qp, ok := ex.(exchange.QuantoMultiplierProvider)
		if !ok {
			continue
		}
		q, ok := qp.GetQuantoMultiplier(symbol)
		if ok && q > 0 && q > maxQ {
			maxQ = q
		}
	}
	return maxQ
}

// roundDownToHundred 向下取整到百位数
// - 如果值 >= 100：向下取整到百位数（例如：1493 -> 1400, 1234.56 -> 1200）
// - 如果值 >= 10 但 < 100：向下取整到十位数（例如：56.7 -> 50, 99.9 -> 90）
// - 如果值 < 10：向下取整（例如：9.8 -> 9, 5.3 -> 5）
func (pm *PositionManager) roundDownToHundred(size float64) float64 {
	if size >= 100.0 {
		// 大于等于 100：向下取整到百位数
		return math.Floor(size/100.0) * 100.0
	} else if size >= 10.0 {
		// 大于等于 10 但小于 100：向下取整到十位数
		return math.Floor(size/10.0) * 10.0
	} else {
		// 小于 10：直接向下取整
		return math.Floor(size)
	}
}

// checkMinTradeValue 检查最小交易价值（至少 50 USDT）
func (pm *PositionManager) checkMinTradeValue(finalSize float64, direction string, exchangePriceData *model.PriceData) bool {
	const minTradeValueUSDT = 50.0 // 最小单次交易价值（USDT）
	var tradeValueUSDT float64

	if direction == "BA" {
		// -A+B: 链上卖出，交易所买入，使用买入价格计算价值
		if exchangePriceData.AskPrice > 0 {
			tradeValueUSDT = finalSize * exchangePriceData.AskPrice
		}
	} else {
		// +A-B: 链上买入，交易所卖出，使用卖出价格计算价值
		if exchangePriceData.BidPrice > 0 {
			tradeValueUSDT = finalSize * exchangePriceData.BidPrice
		}
	}

	if tradeValueUSDT < minTradeValueUSDT {
		pm.logger.Debugf("GetSize: 交易价值 %.2f USDT 小于最小限制 %.2f USDT，返回 0", tradeValueUSDT, minTradeValueUSDT)
		return false
	}

	return true
}

// calculateTradeValue 计算交易价值（USDT）
func (pm *PositionManager) calculateTradeValue(finalSize float64, direction string, exchangePriceData *model.PriceData) float64 {
	if direction == "BA" {
		// -A+B: 链上卖出，交易所买入，使用买入价格计算价值
		if exchangePriceData.AskPrice > 0 {
			return finalSize * exchangePriceData.AskPrice
		}
	} else {
		// +A-B: 链上买入，交易所卖出，使用卖出价格计算价值
		if exchangePriceData.BidPrice > 0 {
			return finalSize * exchangePriceData.BidPrice
		}
	}
	return 0
}

// checkAndRecordCost 检查成本并记录成本数据
// 成本百分比采用 Analytics 公式： (（a滑点+b滑点）/2) + ((a手续费+b手续费)/买一价) 的百分比形式
func (pm *PositionManager) checkAndRecordCost(symbol string, direction string, finalSize float64, exchangePriceData, priceDataForCost *model.PriceData, analyzer analytics.Analyzer) bool {
	// 计算成本（TotalCostPercent = (a滑点+b滑点)/2 + (a手续费+b手续费)/(买一价*size)*100）
	costData := analyzer.CalculateCostForDirection(direction, finalSize, exchangePriceData, priceDataForCost)
	if costData == nil {
		pm.logger.Warnf("GetSize: 成本计算失败，返回 0")
		return false
	}

	costPercentInCoin := costData.TotalCostPercent
	var buyOnePrice float64
	if direction == "BA" {
		buyOnePrice = exchangePriceData.AskPrice // 交易所买入，买一价=Ask
	} else {
		if priceDataForCost != nil {
			buyOnePrice = priceDataForCost.AskPrice // 链上买入，买一价=onchain Ask
		} else {
			buyOnePrice = exchangePriceData.AskPrice // 无链上价格时用交易所 Ask 兜底
		}
	}
	costInCoin := (costPercentInCoin / 100.0) * finalSize
	totalCostUSDT := 0.0
	if buyOnePrice > 0 {
		totalCostUSDT = (costPercentInCoin / 100.0) * buyOnePrice * finalSize
	}

	pm.logger.Debugf("GetSize: 成本计算 - cost%%=%.4f%% (滑点+手续费/买一价), 成本（币）: %.6f, 总成本(USDT): %.2f",
		costPercentInCoin, costInCoin, totalCostUSDT)

	// 记录成本数据到 StatisticsManager
	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager != nil {
		costDataForStats := &statistics.CostData{
			CostInCoin:    costInCoin,
			CostPercent:   costPercentInCoin,
			TotalCostUSDT: totalCostUSDT,
		}
		statisticsManager.RecordCost(symbol, costDataForStats)
	}

	// 可接受的交易成本：使用当前价差阈值（Analytics.GetThreshold）换算成百分比作为上限；
	// 阈值即最小盈利价差，成本需低于该价差对应的百分比才能盈利。阈值未就绪时兜底 1%。
	const defaultMaxCostPercent = 1.0 // 阈值不可用时的兜底最大成本百分比（币本位）
	maxCostPercent := defaultMaxCostPercent
	if thAB, thBA, err := analyzer.GetThreshold(); err == nil {
		var threshold float64
		var price float64
		if direction == "BA" {
			threshold = thBA
			price = exchangePriceData.AskPrice
		} else {
			threshold = thAB
			price = exchangePriceData.BidPrice
		}
		if price > 0 && threshold > 0 {
			maxCostPercent = (threshold / price) * 100.0
		}
	}
	if costPercentInCoin > maxCostPercent {
		pm.logger.Debugf("GetSize: 成本百分比 %.4f%% 超过最大限制 %.2f%%（来自当前阈值），返回 0", costPercentInCoin, maxCostPercent)
		return false
	}

	return true
}

// recordSizeData 记录 Size 数据到 StatisticsManager
func (pm *PositionManager) recordSizeData(symbol string, finalSize, tradeValueUSDT float64) {
	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager != nil {
		sizeDataForStats := &statistics.SizeData{
			Size:     finalSize,
			SizeUSDT: tradeValueUSDT,
		}
		statisticsManager.RecordSize(symbol, sizeDataForStats)
	}
}

// updateSwapInfoAmountLoop 定时更新 swapInfo.Amount 的循环任务（1秒一次）
func (pm *PositionManager) updateSwapInfoAmountLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-pm.ctx.Done():
			return
		case <-ticker.C:
			pm.updateSwapInfoAmountForAllSymbols()
		}
	}
}

// updateSwapInfoAmountForAllSymbols 为所有注册的 symbol 更新 swapInfo.Amount
func (pm *PositionManager) updateSwapInfoAmountForAllSymbols() {
	// 获取所有注册的 symbol
	pm.mu.RLock()
	symbols := make([]string, 0, len(pm.trackedSymbols))
	for symbol := range pm.trackedSymbols {
		symbols = append(symbols, symbol)
	}
	pm.mu.RUnlock()

	if len(symbols) == 0 {
		return
	}

	// 获取 WalletManager 中的 onchain clients
	walletManager := pm.walletManager
	if walletManager == nil {
		return
	}

	walletManager.mu.RLock()
	onchainClients := make(map[string]*OnchainClientConfig)
	for k, v := range walletManager.onchainClients {
		onchainClients[k] = v
	}
	walletManager.mu.RUnlock()

	if len(onchainClients) == 0 {
		return
	}

	// 为每个 symbol 更新 swapInfo.Amount
	for _, symbol := range symbols {
		pm.updateSwapInfoAmountForSymbol(symbol, onchainClients)
	}
}

// updateSwapInfoAmountForSymbol 为指定 symbol 更新 swapInfo.Amount
// 优先使用 Web 配置的固定 size（triggerABSize/triggerBASize），只接受页面设置，不被余额/订单簿覆盖。
func (pm *PositionManager) updateSwapInfoAmountForSymbol(symbol string, onchainClients map[string]*OnchainClientConfig) {
	// 1. 若该 symbol 有 Web 配置的固定 size，直接使用并更新 swapInfo，不再走 GetSize/余额逻辑
	pm.mu.RLock()
	cfgAB := pm.configuredSizeABBySymbol[symbol]
	cfgBA := pm.configuredSizeBABySymbol[symbol]
	registeredTrader := pm.onchainTraders[symbol]
	pm.mu.RUnlock()

	if (cfgAB > 0 || cfgBA > 0) && registeredTrader != nil {
		var size float64
		if cfgAB > 0 && cfgBA > 0 {
			size = math.Min(cfgAB, cfgBA)
		} else if cfgAB > 0 {
			size = cfgAB
		} else {
			size = cfgBA
		}
		coinAmountStr := strconv.FormatFloat(size, 'f', 0, 64)
		registeredTrader.UpdateSwapInfoAmount(coinAmountStr)
		pm.logger.Debugf("updateSwapInfoAmount: %s 使用 Web 配置 size=%.6f，已更新 swapInfo.Amount=%s", symbol, size, coinAmountStr)
		return
	}

	// 2. 无配置 size 时，走原有 GetSize/余额逻辑
	pm.mu.RLock()
	analyzer := pm.analyticsProviders[symbol]
	pm.mu.RUnlock()

	if analyzer == nil {
		pm.logger.Debugf("updateSwapInfoAmount: Symbol %s 没有 Analytics 提供者，跳过", symbol)
		return
	}

	// 获取默认交易所类型（binance）
	exchangeType := "binance"
	chainIndex := "56" // 默认 BSC 链

	// 从价格缓存中获取最新价格
	exchangePriceData := pm.GetLatestPrice(symbol)
	if exchangePriceData == nil || exchangePriceData.BidPrice <= 0 || exchangePriceData.AskPrice <= 0 {
		// 如果没有缓存价格或价格无效，使用默认价格（保守值）
		pm.logger.Debugf("updateSwapInfoAmount: Symbol %s 没有缓存价格数据，使用默认价格 1.0", symbol)
		exchangePriceData = &model.PriceData{
			BidPrice: 1.0,
			AskPrice: 1.0,
		}
	}
	onchainPriceData := exchangePriceData

	// 从注册的 trader 类型中读取，供 GetSize 使用；未注册时传空，走 Legacy 逻辑
	pm.mu.RLock()
	traderA := pm.traderATypeBySymbol[symbol]
	traderB := pm.traderBTypeBySymbol[symbol]
	registeredTrader = pm.onchainTraders[symbol]
	pm.mu.RUnlock()

	// 获取两个方向的 size
	// AB 方向（链上买入）：size 基于 USDT 余额计算
	// BA 方向（链上卖出）：size 基于代币余额计算
	sizeAB := pm.GetSize(symbol, "AB", exchangeType, chainIndex, exchangePriceData, onchainPriceData, traderA, traderB)
	sizeBA := pm.GetSize(symbol, "BA", exchangeType, chainIndex, exchangePriceData, onchainPriceData, traderA, traderB)

	// 🔥 关键修复：使用两个方向的最小值
	// 这样可以确保卖出时不会超过实际代币余额
	// 例如：如果 AB=10000（基于USDT余额），BA=2589（实际代币余额），
	// 应该使用 2589，否则卖出查询会使用 10000 导致余额不足
	var size float64
	if sizeAB > 0 && sizeBA > 0 {
		size = math.Min(sizeAB, sizeBA)
		pm.logger.Debugf("updateSwapInfoAmount: Symbol %s - AB size: %.6f, BA size: %.6f, 使用最小值: %.6f",
			symbol, sizeAB, sizeBA, size)
	} else if sizeAB > 0 {
		size = sizeAB
		pm.logger.Debugf("updateSwapInfoAmount: Symbol %s - 只有 AB 方向有效，使用 AB size: %.6f", symbol, sizeAB)
	} else if sizeBA > 0 {
		size = sizeBA
		pm.logger.Debugf("updateSwapInfoAmount: Symbol %s - 只有 BA 方向有效，使用 BA size: %.6f", symbol, sizeBA)
	}

	if size <= 0 {
		pm.logger.Debugf("updateSwapInfoAmount: Symbol %s 的 GetSize 返回 0，跳过更新", symbol)
		return
	}

	// 更新到该 symbol 对应的 onchain client 的 swapInfo.Amount
	// 优先使用注册的 onchain client（来自 Trigger），如果没有则使用 WalletManager 中的

	// 🔥 关键检查：确保 size 对应的 USDT 价值不低于 200 USDT
	const minUSDTValue = 200.0
	const maxUSDTValue = 2000.0
	sizeValueUSDT := size * exchangePriceData.BidPrice
	if sizeValueUSDT < minUSDTValue {
		pm.logger.Debugf("updateSwapInfoAmount: Symbol %s 的 size %.6f 对应 USDT 价值 %.2f 低于最小限制 %.2f，跳过更新",
			symbol, size, sizeValueUSDT, minUSDTValue)
		return
	}
	// 如果超过最大值，限制 size
	if sizeValueUSDT > maxUSDTValue {
		limitedSize := maxUSDTValue / exchangePriceData.BidPrice
		pm.logger.Debugf("updateSwapInfoAmount: Symbol %s 的 size %.6f 对应 USDT 价值 %.2f 超过最大限制 %.2f，限制为 %.6f",
			symbol, size, sizeValueUSDT, maxUSDTValue, limitedSize)
		size = limitedSize
	}

	// 检查 GetSize 返回值是否异常
	if size * exchangePriceData.BidPrice > 10000 {
		pm.logger.Warnf("⚠️ updateSwapInfoAmount: Symbol %s 的 GetSize 返回异常大的值: %.2f (可能导致余额不足)", symbol, size)
	}
	
	coinAmountStr := strconv.FormatFloat(size, 'f', 0, 64)

	pm.mu.RLock()
	registeredTrader = pm.onchainTraders[symbol]
	pm.mu.RUnlock()

	if registeredTrader != nil {
		// 使用注册的 onchain trader（来自 Trigger）
		registeredTrader.UpdateSwapInfoAmount(coinAmountStr)
		pm.logger.Debugf("updateSwapInfoAmount: 已更新 %s 的 swapInfo.Amount 为 %s (size=%.6f) [使用注册的 client]", symbol, coinAmountStr, size)
	}
}

// extractBaseSymbol 从交易对 symbol 中提取基础币种
// 这个函数在 price_provider.go 中已删除，需要重新实现
// 若 symbol 不含常见计价后缀，则视为已是基础币种并直接返回。
func extractBaseSymbol(symbol string) string {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return ""
	}

	upper := strings.ToUpper(symbol)
	suffixes := []string{"USDT", "USDC", "BUSD", "BTC", "ETH"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(upper, suffix) && len(symbol) > len(suffix) {
			return symbol[:len(symbol)-len(suffix)]
		}
	}
	// 未匹配到计价币后缀，认为传入已是基础币种
	return symbol
}


