package statistics

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/utils/notify/telegram"
	"auto-arbitrage/internal/utils/parallel"
	"auto-arbitrage/internal/web/ws"

	"go.uber.org/zap"
)

func writeStatsDebug(location, message, hypothesisId string, data map[string]interface{}) {}

// StatisticsManager 统计管理器（单例）
// 负责收集和统计交易相关的数据，用于监控展示
type StatisticsManager struct {
	mu sync.RWMutex

	// 按 symbol 存储统计数据
	symbolStats map[string]*SymbolStatistics

	// 定时记录协程
	ctx    context.Context
	cancel context.CancelFunc

	// 记录间隔（500ms）
	recordInterval time.Duration

	// 协程组
	routineGroup *parallel.RoutineGroup

	// 日志实例
	logger *zap.SugaredLogger

	// WebSocket Hub（用于实时推送）
	wsHub *ws.Hub

	// 是否已启动
	isRunning bool

	// 数据持久化目录
	dataDir string

	// 资产历史数据目录
	assetDataDir string

	// 资产记录器专用锁（保护 isAssetRecorderRunning 和 latestWalletInfo）
	assetMu sync.RWMutex

	// 资产记录是否运行中
	isAssetRecorderRunning bool

	// 最新钱包信息（用于资产记录）
	latestWalletInfo *model.WalletDetailInfo
}

// AssetSnapshot 资产快照
type AssetSnapshot struct {
	Timestamp          int64              `json:"timestamp"`          // Unix timestamp
	TotalAsset         float64            `json:"totalAsset"`         // 总资产
	TotalUnrealizedPnl float64            `json:"totalUnrealizedPnl"` // 总未实现盈亏
	ExchangeAsset      float64            `json:"exchangeAsset"`      // 交易所资产
	OnchainAsset       float64            `json:"onchainAsset"`       // 链上资产
	Details            map[string]float64 `json:"details"`            // 各交易所/链上明细
}

// SymbolStatistics 单个 symbol 的统计数据
type SymbolStatistics struct {
	Symbol string

	mu sync.RWMutex

	// 时间序列数据（每500ms记录一次）
	TimeSeriesData []*TimeSeriesPoint

	// 滑点统计（两边）
	SlippageStats *SlippageStatistics

	// 成本统计
	CostStats *CostStatistics

	// Size统计
	SizeStats *SizeStatistics

	// 成交数据（实时记录）
	TradeRecords []*TradeRecord

	// 价差数据（时间序列）
	PriceDiffRecords []*PriceDiffRecord

	// 价格数据（时间序列）
	PriceRecords []*PriceRecord

	// Trigger统计
	TriggerStats *TriggerStatistics

	// 钱包统计（最新）
	WalletStats *WalletStatistics

	// 临时数据（用于定时记录）
	tempSlippageData *SlippageData
	tempCostData     *CostData
	tempSizeData     *SizeData
	tempPriceDiff    *PriceDiffData
	tempPrice        *PriceData
}

// TimeSeriesPoint 时间序列数据点
type TimeSeriesPoint struct {
	Timestamp time.Time

	// 价差数据
	DiffAB float64
	DiffBA float64

	// 价格数据
	ExchangeBidPrice float64
	ExchangeAskPrice float64
	OnchainBidPrice  float64
	OnchainAskPrice  float64

	// 滑点数据
	ExchangeBuySlippage  float64
	ExchangeSellSlippage float64
	OnchainBuySlippage   float64
	OnchainSellSlippage  float64

	// 成本数据
	CostInCoin    float64
	CostPercent   float64
	TotalCostUSDT float64

	// Size数据
	Size     float64
	SizeUSDT float64
}

// SlippageStatistics 滑点统计
type SlippageStatistics struct {
	ExchangeBuy  *StatisticValues // 交易所买入滑点（已废弃，使用 ABuy/BBuy）
	ExchangeSell *StatisticValues // 交易所卖出滑点（已废弃，使用 ASell/BSell）
	OnChainBuy   *StatisticValues // 链上买入滑点（已废弃，使用 ABuy/BBuy）
	OnChainSell  *StatisticValues // 链上卖出滑点（已废弃，使用 ASell/BSell）

	// A 和 B 的滑点统计（新的统一字段）
	ABuy  *StatisticValues // A 买入滑点
	ASell *StatisticValues // A 卖出滑点
	BBuy  *StatisticValues // B 买入滑点
	BSell *StatisticValues // B 卖出滑点
}

// CostStatistics 成本统计
type CostStatistics struct {
	CostInCoin  *StatisticValues // 成本（币数量）
	CostPercent *StatisticValues // 成本百分比
}

// SizeStatistics Size统计
type SizeStatistics struct {
	Size     *StatisticValues // Size（币数量）
	SizeUSDT *StatisticValues // Size（USDT价值）
}

// StatisticValues 统计值集合（用于计算平均值、最大值、最小值、90分位值）
type StatisticValues struct {
	mu     sync.RWMutex
	Values []float64
}

// TradeRecord 成交记录（覆盖数量、成交量、磨损、gas 等）
type TradeRecord struct {
	Timestamp   time.Time
	Direction   string  // "AB" 或 "BA"
	Size        float64 // 计划数量
	SizeUSDT    float64
	Price       float64
	DiffValue   float64
	Profit      float64 // 收益/亏损（USDT）
	CostInCoin  float64
	CostPercent float64 // 磨损百分比

	// A/B 成交明细
	FilledQtyA   float64 // A 端成交量
	FilledQtyB   float64 // B 端成交量
	FilledPriceA float64
	FilledPriceB float64
	FeeA         float64
	FeeB         float64
	GasA         float64
	GasB         float64
	RevenueA     float64
	CostA        float64
	RevenueB     float64
	CostB        float64
}

// PriceDiffRecord 价差记录
type PriceDiffRecord struct {
	Timestamp time.Time
	DiffAB    float64
	DiffBA    float64
}

// PriceRecord 价格记录
type PriceRecord struct {
	Timestamp   time.Time
	ExchangeBid float64
	ExchangeAsk float64
	OnchainBid  float64
	OnchainAsk  float64
}

// SlippageData 滑点数据（临时）
type SlippageData struct {
	ExchangeBuy  float64 // 已废弃，使用 ABuy/BBuy
	ExchangeSell float64 // 已废弃，使用 ASell/BSell
	OnChainBuy   float64 // 已废弃，使用 ABuy/BBuy
	OnChainSell  float64 // 已废弃，使用 ASell/BSell

	// A 和 B 的滑点（新的统一字段）
	ABuy  float64
	ASell float64
	BBuy  float64
	BSell float64
}

// CostData 成本数据（临时）
type CostData struct {
	CostInCoin    float64
	CostPercent   float64
	TotalCostUSDT float64
}

// SizeData Size数据（临时）
type SizeData struct {
	Size     float64
	SizeUSDT float64
}

// PriceDiffData 价差数据（临时）
type PriceDiffData struct {
	DiffAB float64
	DiffBA float64
}

// PriceData 价格数据（临时）
type PriceData struct {
	ExchangeBid float64
	ExchangeAsk float64
	OnchainBid  float64
	OnchainAsk  float64
}

// TriggerStatistics Trigger统计
type TriggerStatistics struct {
	mu sync.RWMutex

	TotalTrades     int     // 总成交次数
	TotalVolume     float64 // 总交易量（币数量）
	TotalVolumeUSDT float64 // 总交易量（USDT）
	TotalProfit     float64 // 总收益（USDT，正数为盈利，负数为亏损）
	WinCount        int     // 盈利次数
	LossCount       int     // 亏损次数
}

// WalletStatistics 钱包统计
type WalletStatistics struct {
	mu sync.RWMutex

	TotalAsset         float64   // 总资产
	TotalUnrealizedPnl float64   // 总未实现盈亏
	TotalPositionValue float64   // 总持仓价值
	TotalBalanceValue  float64   // 总余额价值
	TotalOnchainValue  float64   // 总链上余额价值
	UpdateTime         time.Time // 更新时间
}

// 全局单例
var globalStatisticsManager *StatisticsManager
var globalStatisticsManagerOnce sync.Once

// GetStatisticsManager 获取全局统计管理器实例（单例）
func GetStatisticsManager() *StatisticsManager {
	globalStatisticsManagerOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		globalStatisticsManager = &StatisticsManager{
			symbolStats:    make(map[string]*SymbolStatistics),
			ctx:            ctx,
			cancel:         cancel,
			recordInterval: 500 * time.Millisecond,
			routineGroup:   parallel.NewRoutineGroup(),
			logger:         logger.GetLoggerInstance().Named("StatisticsManager").Sugar(),
			dataDir:        "data/history/statistics",
			assetDataDir:   "data/history/assets",
		}
		// 确保目录存在
		_ = os.MkdirAll(globalStatisticsManager.dataDir, 0755)
		_ = os.MkdirAll(globalStatisticsManager.assetDataDir, 0755)
	})
	return globalStatisticsManager
}

// Start 启动统计管理器
func (sm *StatisticsManager) Start() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.isRunning {
		sm.logger.Warn("StatisticsManager 已经在运行中")
		return
	}

	sm.isRunning = true
	sm.logger.Info("StatisticsManager 已启动")

	// 启动定时记录协程
	sm.routineGroup.GoSafe(func() {
		sm.recordLoop()
	})

	// 启动资产记录协程
	sm.StartAssetRecorder()
}

// Stop 停止统计管理器
func (sm *StatisticsManager) Stop() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if !sm.isRunning {
		return
	}

	sm.isRunning = false

	sm.assetMu.Lock()
	sm.isAssetRecorderRunning = false
	sm.assetMu.Unlock()

	sm.cancel()
	sm.routineGroup.Wait()

	sm.logger.Info("StatisticsManager 已停止")
}

// recordLoop 定时记录循环（每500ms记录一次）
func (sm *StatisticsManager) recordLoop() {
	ticker := time.NewTicker(sm.recordInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sm.ctx.Done():
			return
		case <-ticker.C:
			sm.recordAllSymbols()
		}
	}
}

// StartAssetRecorder 启动资产记录器
func (sm *StatisticsManager) StartAssetRecorder() {
	sm.assetMu.Lock()
	if sm.isAssetRecorderRunning {
		sm.assetMu.Unlock()
		return
	}
	sm.isAssetRecorderRunning = true
	sm.assetMu.Unlock()

	sm.routineGroup.GoSafe(func() {
		sm.assetRecordLoop()
	})
}

// assetRecordLoop 资产记录循环（每30秒记录一次）
func (sm *StatisticsManager) assetRecordLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	sm.logger.Info("资产历史记录器已启动，频率: 30s")

	for {
		select {
		case <-sm.ctx.Done():
			return
		case <-ticker.C:
			sm.assetMu.RLock()
			running := sm.isAssetRecorderRunning
			sm.assetMu.RUnlock()

			if !running {
				return
			}
			sm.recordAssetSnapshot()
		}
	}
}

// recordAssetSnapshot 记录资产快照
func (sm *StatisticsManager) recordAssetSnapshot() {
	sm.assetMu.RLock()
	walletInfo := sm.latestWalletInfo
	sm.assetMu.RUnlock()

	if walletInfo == nil {
		return
	}

	// 异常过滤：如果总资产为0，跳过
	if walletInfo.TotalAsset <= 0 {
		return
	}

	// 收集明细数据
	details := make(map[string]float64)

	// 记录各个交易所的净资产 (Balance + UnPnl)
	if walletInfo.ExchangeWallets != nil {
		for name, wallet := range walletInfo.ExchangeWallets {
			if wallet != nil {
				// 同样修正：单交易所资产 = 余额 + 未实现盈亏
				val := wallet.TotalBalanceValue + wallet.TotalUnrealizedPnl
				details[name] = val
			}
		}
	}

	// 记录链上资产
	details["onchain"] = walletInfo.TotalOnchainValue

	// DEBUG: 打印详情以便调试
	sm.logger.Infof("Recording asset snapshot: Total=%.2f, Details=%v", walletInfo.TotalAsset, details)

	snapshot := AssetSnapshot{
		Timestamp:          time.Now().Unix(),
		TotalAsset:         walletInfo.TotalAsset,
		TotalUnrealizedPnl: walletInfo.TotalUnrealizedPnl,
		ExchangeAsset:      walletInfo.TotalBalanceValue + walletInfo.TotalUnrealizedPnl, // 这里也同步修正
		OnchainAsset:       walletInfo.TotalOnchainValue,
		Details:            details,
	}

	// 确定文件名：按 UTC+8 日期
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	dateStr := time.Now().In(loc).Format("2006-01-02")
	filename := filepath.Join(sm.assetDataDir, fmt.Sprintf("%s.json", dateStr))

	// 读取现有数据（如果文件存在）
	var snapshots []AssetSnapshot
	if _, err := os.Stat(filename); err == nil {
		data, err := ioutil.ReadFile(filename)
		if err == nil {
			_ = json.Unmarshal(data, &snapshots)
		}
	}

	// 追加新记录
	snapshots = append(snapshots, snapshot)

	// 写入文件
	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		sm.logger.Errorf("Failed to marshal asset snapshots: %v", err)
		return
	}

	if err := ioutil.WriteFile(filename, data, 0644); err != nil {
		sm.logger.Errorf("Failed to write asset snapshots to %s: %v", filename, err)
	}
}

// recordAllSymbols 记录所有 symbol 的数据
func (sm *StatisticsManager) recordAllSymbols() {
	sm.mu.RLock()
	symbols := make([]string, 0, len(sm.symbolStats))
	for symbol := range sm.symbolStats {
		symbols = append(symbols, symbol)
	}
	sm.mu.RUnlock()

	for _, symbol := range symbols {
		sm.recordSymbolData(symbol)
	}
}

// recordSymbolData 记录单个 symbol 的数据
func (sm *StatisticsManager) recordSymbolData(symbol string) {
	sm.mu.RLock()
	stats, exists := sm.symbolStats[symbol]
	sm.mu.RUnlock()

	if !exists || stats == nil {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	// 创建时间序列数据点
	point := &TimeSeriesPoint{
		Timestamp: time.Now(),
	}

	// 记录价差数据
	if stats.tempPriceDiff != nil {
		point.DiffAB = stats.tempPriceDiff.DiffAB
		point.DiffBA = stats.tempPriceDiff.DiffBA

		// 添加到价差记录
		stats.PriceDiffRecords = append(stats.PriceDiffRecords, &PriceDiffRecord{
			Timestamp: point.Timestamp,
			DiffAB:    point.DiffAB,
			DiffBA:    point.DiffBA,
		})
	}

	// 记录价格数据
	if stats.tempPrice != nil {
		point.ExchangeBidPrice = stats.tempPrice.ExchangeBid
		point.ExchangeAskPrice = stats.tempPrice.ExchangeAsk
		point.OnchainBidPrice = stats.tempPrice.OnchainBid
		point.OnchainAskPrice = stats.tempPrice.OnchainAsk

		// 添加到价格记录
		stats.PriceRecords = append(stats.PriceRecords, &PriceRecord{
			Timestamp:   point.Timestamp,
			ExchangeBid: point.ExchangeBidPrice,
			ExchangeAsk: point.ExchangeAskPrice,
			OnchainBid:  point.OnchainBidPrice,
			OnchainAsk:  point.OnchainAskPrice,
		})
	}

	// 记录滑点数据
	if stats.tempSlippageData != nil {
		point.ExchangeBuySlippage = stats.tempSlippageData.ExchangeBuy
		point.ExchangeSellSlippage = stats.tempSlippageData.ExchangeSell
		point.OnchainBuySlippage = stats.tempSlippageData.OnChainBuy
		point.OnchainSellSlippage = stats.tempSlippageData.OnChainSell

		// 添加到统计值（向后兼容）
		stats.SlippageStats.ExchangeBuy.AddValue(point.ExchangeBuySlippage)
		stats.SlippageStats.ExchangeSell.AddValue(point.ExchangeSellSlippage)
		stats.SlippageStats.OnChainBuy.AddValue(point.OnchainBuySlippage)
		stats.SlippageStats.OnChainSell.AddValue(point.OnchainSellSlippage)

		// 添加 A 和 B 的滑点统计
		stats.SlippageStats.ABuy.AddValue(stats.tempSlippageData.ABuy)
		stats.SlippageStats.ASell.AddValue(stats.tempSlippageData.ASell)
		stats.SlippageStats.BBuy.AddValue(stats.tempSlippageData.BBuy)
		stats.SlippageStats.BSell.AddValue(stats.tempSlippageData.BSell)
	}

	// 记录成本数据
	if stats.tempCostData != nil {
		point.CostInCoin = stats.tempCostData.CostInCoin
		point.CostPercent = stats.tempCostData.CostPercent
		point.TotalCostUSDT = stats.tempCostData.TotalCostUSDT

		// 添加到统计值
		stats.CostStats.CostInCoin.AddValue(point.CostInCoin)
		stats.CostStats.CostPercent.AddValue(point.CostPercent)
	}

	// 记录Size数据
	if stats.tempSizeData != nil {
		point.Size = stats.tempSizeData.Size
		point.SizeUSDT = stats.tempSizeData.SizeUSDT

		// 添加到统计值
		stats.SizeStats.Size.AddValue(point.Size)
		stats.SizeStats.SizeUSDT.AddValue(point.SizeUSDT)
	}

	// 添加到时间序列数据
	stats.TimeSeriesData = append(stats.TimeSeriesData, point)

	// 限制时间序列数据长度（保留最近的数据，例如最近1小时的数据）
	maxDataPoints := 7200 // 500ms * 7200 = 1小时
	if len(stats.TimeSeriesData) > maxDataPoints {
		stats.TimeSeriesData = stats.TimeSeriesData[len(stats.TimeSeriesData)-maxDataPoints:]
	}
	if len(stats.PriceDiffRecords) > maxDataPoints {
		stats.PriceDiffRecords = stats.PriceDiffRecords[len(stats.PriceDiffRecords)-maxDataPoints:]
	}
	if len(stats.PriceRecords) > maxDataPoints {
		stats.PriceRecords = stats.PriceRecords[len(stats.PriceRecords)-maxDataPoints:]
	}
}

// LoadSymbolData 从文件加载 symbol 数据
func (sm *StatisticsManager) LoadSymbolData(symbol string) {
	filename := filepath.Join(sm.dataDir, fmt.Sprintf("%s.json", symbol))
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return
	}

	data, err := ioutil.ReadFile(filename)
	if err != nil {
		sm.logger.Warnf("加载统计数据失败 %s: %v", symbol, err)
		return
	}

	var savedStats SymbolStatistics
	if err := json.Unmarshal(data, &savedStats); err != nil {
		sm.logger.Warnf("解析统计数据失败 %s: %v", symbol, err)
		return
	}

	// 恢复数据
	sm.mu.Lock()
	stats, exists := sm.symbolStats[symbol]
	if !exists {
		// 如果还没注册，理论上不应该发生，因为 Load 是在 Register 后调用的
		// 但为了安全，这里可以初始化
		sm.mu.Unlock() // 释放锁再调用 Register 防止死锁
		sm.RegisterSymbol(symbol)
		sm.mu.Lock()
		stats = sm.symbolStats[symbol]
	}
	sm.mu.Unlock()

	if stats != nil {
		stats.mu.Lock()
		defer stats.mu.Unlock()

		// 恢复关键数据
		stats.TradeRecords = savedStats.TradeRecords
		stats.TriggerStats = savedStats.TriggerStats
		// 恢复 TimeSeriesData 等（如果需要）
		// 注意：SlippageStats 等统计值如果是复杂结构体，json unmarshal 可能会有问题，因为 StatisticValues 里的 Values 是私有的？
		// StatisticValues 的 Values 是导出的 (Public)，所以可以直接恢复。
		if savedStats.SlippageStats != nil {
			stats.SlippageStats = savedStats.SlippageStats
			// 重新初始化 Mutex (Mutex 不会被 JSON 序列化/反序列化，状态是零值，即未锁定，这是对的)
		}
		if savedStats.CostStats != nil {
			stats.CostStats = savedStats.CostStats
		}
		if savedStats.SizeStats != nil {
			stats.SizeStats = savedStats.SizeStats
		}

		sm.logger.Infof("已恢复 symbol 统计数据: %s (成交记录: %d)", symbol, len(stats.TradeRecords))
	}
}

// SaveSymbolData 保存 symbol 数据到文件
func (sm *StatisticsManager) SaveSymbolData(symbol string) {
	sm.mu.RLock()
	stats, exists := sm.symbolStats[symbol]
	sm.mu.RUnlock()

	if !exists || stats == nil {
		return
	}

	stats.mu.RLock()
	// 序列化
	data, err := json.MarshalIndent(stats, "", "  ")
	stats.mu.RUnlock()

	if err != nil {
		sm.logger.Errorf("序列化统计数据失败 %s: %v", symbol, err)
		return
	}

	filename := filepath.Join(sm.dataDir, fmt.Sprintf("%s.json", symbol))
	if err := ioutil.WriteFile(filename, data, 0644); err != nil {
		sm.logger.Errorf("保存统计数据失败 %s: %v", symbol, err)
	}
}

// ClearSymbolHistory 清除 symbol 历史数据
func (sm *StatisticsManager) ClearSymbolHistory(symbol string) error {
	// 1. 清除内存数据
	sm.mu.RLock()
	stats, exists := sm.symbolStats[symbol]
	sm.mu.RUnlock()

	if exists && stats != nil {
		stats.mu.Lock()
		stats.TradeRecords = make([]*TradeRecord, 0)
		stats.TriggerStats = newTriggerStatistics()
		stats.TimeSeriesData = make([]*TimeSeriesPoint, 0)
		stats.SlippageStats = newSlippageStatistics()
		stats.CostStats = newCostStatistics()
		stats.SizeStats = newSizeStatistics()
		stats.PriceDiffRecords = make([]*PriceDiffRecord, 0)
		stats.PriceRecords = make([]*PriceRecord, 0)
		stats.mu.Unlock()
	}

	// 2. 删除文件
	filename := filepath.Join(sm.dataDir, fmt.Sprintf("%s.json", symbol))
	if err := os.Remove(filename); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除统计文件失败: %v", err)
	}

	sm.logger.Infof("已清除 symbol 历史数据: %s", symbol)
	return nil
}

// RegisterSymbol 注册需要统计的 symbol
func (sm *StatisticsManager) RegisterSymbol(symbol string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.symbolStats[symbol] != nil {
		return
	}

	sm.symbolStats[symbol] = &SymbolStatistics{
		Symbol:           symbol,
		TimeSeriesData:   make([]*TimeSeriesPoint, 0),
		SlippageStats:    newSlippageStatistics(),
		CostStats:        newCostStatistics(),
		SizeStats:        newSizeStatistics(),
		TradeRecords:     make([]*TradeRecord, 0),
		PriceDiffRecords: make([]*PriceDiffRecord, 0),
		PriceRecords:     make([]*PriceRecord, 0),
		TriggerStats:     newTriggerStatistics(),
		WalletStats:      &WalletStatistics{},
	}

	sm.logger.Infof("已注册 symbol 统计: %s", symbol)

	// 尝试加载历史数据
	// 注意：这里不能直接调用 LoadSymbolData，因为 LoadSymbolData 会获取 sm.mu 锁（在内部），造成死锁
	// 实际上 LoadSymbolData 获取 sm.mu 只是为了拿到 stats，而这里我们已经持有 sm.mu 并且刚刚创建了 stats
	// 所以我们可以直接在这里调用内部加载逻辑，或者启动一个 goroutine 去加载
	// 为了简单和安全，我们启动一个 goroutine 去加载
	go sm.LoadSymbolData(symbol)
}

// UnregisterSymbol 取消注册 symbol
func (sm *StatisticsManager) UnregisterSymbol(symbol string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	delete(sm.symbolStats, symbol)
	sm.logger.Infof("已取消注册 symbol 统计: %s", symbol)
}

// SetWSHub 设置 WebSocket Hub
func (sm *StatisticsManager) SetWSHub(hub *ws.Hub) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.wsHub = hub
}

// RecordSlippage 记录滑点数据（由外部调用，更新临时数据）
func (sm *StatisticsManager) RecordSlippage(symbol string, slippageData *SlippageData) {
	sm.mu.RLock()
	stats, exists := sm.symbolStats[symbol]
	sm.mu.RUnlock()

	if !exists || stats == nil {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	stats.tempSlippageData = slippageData
}

// RecordCost 记录成本数据（由外部调用，更新临时数据）
func (sm *StatisticsManager) RecordCost(symbol string, costData *CostData) {
	sm.mu.RLock()
	stats, exists := sm.symbolStats[symbol]
	sm.mu.RUnlock()

	if !exists || stats == nil {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	stats.tempCostData = costData
}

// RecordSize 记录Size数据（由外部调用，更新临时数据）
func (sm *StatisticsManager) RecordSize(symbol string, sizeData *SizeData) {
	sm.mu.RLock()
	stats, exists := sm.symbolStats[symbol]
	sm.mu.RUnlock()

	if !exists || stats == nil {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	stats.tempSizeData = sizeData
}

// RecordPriceDiff 记录价差数据（由外部调用，更新临时数据）
func (sm *StatisticsManager) RecordPriceDiff(symbol string, diffAB, diffBA float64) {
	sm.mu.RLock()
	stats, exists := sm.symbolStats[symbol]
	sm.mu.RUnlock()

	if !exists || stats == nil {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	stats.tempPriceDiff = &PriceDiffData{
		DiffAB: diffAB,
		DiffBA: diffBA,
	}

	// 实时推送价差数据
	if sm.wsHub != nil {
		msg := map[string]interface{}{
			"type": "price_diff_update",
			"data": map[string]interface{}{
				"symbol": symbol,
				"diffAB": diffAB,
				"diffBA": diffBA,
				"time":   time.Now().Unix(),
			},
		}
		if bytes, err := json.Marshal(msg); err == nil {
			sm.wsHub.Broadcast(bytes)
		}
	}
}

// GetLatestPriceDiff 获取最新的价差数据
func (sm *StatisticsManager) GetLatestPriceDiff(symbol string) (diffAB, diffBA float64, exists bool) {
	sm.mu.RLock()
	stats, exists := sm.symbolStats[symbol]
	sm.mu.RUnlock()

	if !exists || stats == nil {
		return 0, 0, false
	}

	stats.mu.RLock()
	defer stats.mu.RUnlock()

	if stats.tempPriceDiff == nil {
		return 0, 0, false
	}

	return stats.tempPriceDiff.DiffAB, stats.tempPriceDiff.DiffBA, true
}

// RecordPrice 记录价格数据（由外部调用，更新临时数据）
func (sm *StatisticsManager) RecordPrice(symbol string, priceData *PriceData) {
	sm.mu.RLock()
	stats, exists := sm.symbolStats[symbol]
	sm.mu.RUnlock()

	if !exists || stats == nil {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	stats.tempPrice = priceData
}

// RecordTrade 记录成交数据（实时记录）
func (sm *StatisticsManager) RecordTrade(symbol string, trade *TradeRecord) {
	sm.mu.RLock()
	stats, exists := sm.symbolStats[symbol]
	sm.mu.RUnlock()
	// #region agent log
	writeStatsDebug("statistics.go:RecordTrade", "entry", "H2", map[string]interface{}{"symbol": symbol, "exists": exists, "statsNil": stats == nil})
	// #endregion
	if !exists || stats == nil {
		// #region agent log
		writeStatsDebug("statistics.go:RecordTrade", "return early symbol not registered", "H2", map[string]interface{}{"symbol": symbol})
		// #endregion
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	trade.Timestamp = time.Now()
	stats.TradeRecords = append(stats.TradeRecords, trade)

	// 更新Trigger统计
	stats.TriggerStats.TotalTrades++
	stats.TriggerStats.TotalVolume += trade.Size
	stats.TriggerStats.TotalVolumeUSDT += trade.SizeUSDT
	stats.TriggerStats.TotalProfit += trade.Profit
	if trade.Profit > 0 {
		stats.TriggerStats.WinCount++
	} else if trade.Profit < 0 {
		stats.TriggerStats.LossCount++
	}

	// 限制成交记录长度（保留最近的数据）
	maxTradeRecords := 1000
	if len(stats.TradeRecords) > maxTradeRecords {
		stats.TradeRecords = stats.TradeRecords[len(stats.TradeRecords)-maxTradeRecords:]
	}

	// 异步发送 Telegram 消息
	go func(s string, t *TradeRecord) {
		if telegram.GlobalTgBotClient == nil {
			return
		}

		profitIcon := "🟢"
		if t.Profit < 0 {
			profitIcon = "🔴"
		}

		msg := fmt.Sprintf("<b>【交易结果统计】</b> %s\n\n", profitIcon)
		msg += fmt.Sprintf("💱 <b>交易对:</b> %s (%s)\n", s, t.Direction)
		msg += fmt.Sprintf("📦 <b>数量:</b> %.6f (%.2f USDT)\n", t.Size, t.SizeUSDT)
		msg += fmt.Sprintf("📊 <b>成交价:</b> %.6f\n", t.Price)
		msg += fmt.Sprintf("📉 <b>价差:</b> %.4f%%\n", t.DiffValue)
		msg += fmt.Sprintf("💰 <b>盈亏:</b> %.2f USDT\n", t.Profit)
		msg += fmt.Sprintf("💸 <b>成本:</b> %.4f%% (%.6f Coin)\n", t.CostPercent, t.CostInCoin)
		msg += fmt.Sprintf("\n⏱ <b>时间:</b> %s", t.Timestamp.Format("15:04:05.000"))

		_, err := telegram.GlobalTgBotClient.SendHTMLMessage(msg)
		if err != nil {
			sm.logger.Warnf("Telegram通知失败: %v", err)
		}

		// 触发保存数据
		sm.SaveSymbolData(s)
	}(symbol, trade)
}

// GetLatestTotalAsset 返回最近一次 RecordWallet 的总资产（USDT），无数据时返回 0
func (sm *StatisticsManager) GetLatestTotalAsset() float64 {
	sm.assetMu.Lock()
	defer sm.assetMu.Unlock()
	if sm.latestWalletInfo == nil {
		return 0
	}
	return sm.latestWalletInfo.TotalAsset
}

// RecordWallet 记录钱包信息
func (sm *StatisticsManager) RecordWallet(walletInfo *model.WalletDetailInfo) {
	sm.assetMu.Lock()
	// 更新全局钱包信息缓存
	sm.latestWalletInfo = walletInfo
	sm.assetMu.Unlock()

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// 更新所有 symbol 的钱包统计
	for _, stats := range sm.symbolStats {
		stats.mu.Lock()
		if stats.WalletStats == nil {
			stats.WalletStats = &WalletStatistics{}
		}
		stats.WalletStats.mu.Lock()
		stats.WalletStats.TotalAsset = walletInfo.TotalAsset
		stats.WalletStats.TotalUnrealizedPnl = walletInfo.TotalUnrealizedPnl
		stats.WalletStats.TotalPositionValue = walletInfo.TotalPositionValue
		stats.WalletStats.TotalBalanceValue = walletInfo.TotalBalanceValue
		stats.WalletStats.TotalOnchainValue = walletInfo.TotalOnchainValue
		stats.WalletStats.UpdateTime = walletInfo.UpdateTime
		stats.WalletStats.mu.Unlock()
		stats.mu.Unlock()
	}
}

// GetSymbolStatistics 获取 symbol 的统计数据
func (sm *StatisticsManager) GetSymbolStatistics(symbol string) *SymbolStatistics {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return sm.symbolStats[symbol]
}

// GetPriceRecords 获取 symbol 的价格记录（线程安全）
func (sm *StatisticsManager) GetPriceRecords(symbol string) []*PriceRecord {
	stats := sm.GetSymbolStatistics(symbol)
	if stats == nil {
		return nil
	}
	stats.mu.RLock()
	defer stats.mu.RUnlock()

	// 返回副本以避免并发问题
	result := make([]*PriceRecord, len(stats.PriceRecords))
	copy(result, stats.PriceRecords)
	return result
}

// GetRecentTradeRecords 返回 symbol 最近 limit 条成交记录，按时间倒序（最新在前）
func (sm *StatisticsManager) GetRecentTradeRecords(symbol string, limit int) []*TradeRecord {
	// #region agent log
	writeStatsDebug("statistics.go:GetRecentTradeRecords", "entry", "H2", map[string]interface{}{"symbol": symbol, "limit": limit})
	// #endregion
	stats := sm.GetSymbolStatistics(symbol)
	if stats == nil {
		// #region agent log
		writeStatsDebug("statistics.go:GetRecentTradeRecords", "stats nil", "H2", map[string]interface{}{"symbol": symbol})
		// #endregion
		return nil
	}
	stats.mu.RLock()
	defer stats.mu.RUnlock()
	n := len(stats.TradeRecords)
	// #region agent log
	writeStatsDebug("statistics.go:GetRecentTradeRecords", "records count", "H3", map[string]interface{}{"symbol": symbol, "recordsLen": n})
	// #endregion
	if n == 0 {
		return nil
	}
	start := 0
	if n > limit {
		start = n - limit
	}
	out := make([]*TradeRecord, 0, limit)
	for i := n - 1; i >= start; i-- {
		out = append(out, stats.TradeRecords[i])
	}
	return out
}

// GetAllStatistics 获取所有统计数据
func (sm *StatisticsManager) GetAllStatistics() map[string]*SymbolStatistics {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make(map[string]*SymbolStatistics)
	for symbol, stats := range sm.symbolStats {
		result[symbol] = stats
	}
	return result
}

// StatisticsResponse 统计数据响应结构
type StatisticsResponse struct {
	Symbol string `json:"symbol"`

	// 滑点统计
	SlippageStats map[string]map[string]float64 `json:"slippageStats,omitempty"`

	// 成本统计
	CostStats map[string]map[string]float64 `json:"costStats,omitempty"`

	// Size统计
	SizeStats map[string]map[string]float64 `json:"sizeStats,omitempty"`

	// Trigger统计
	TriggerStats map[string]interface{} `json:"triggerStats,omitempty"`

	// 时间序列数据
	TradeRecords     []map[string]interface{} `json:"tradeRecords,omitempty"`
	PriceDiffRecords []map[string]interface{} `json:"priceDiffRecords,omitempty"`
	PriceRecords     []map[string]interface{} `json:"priceRecords,omitempty"`
}

// GetAssetHistory 获取资产历史数据
func (sm *StatisticsManager) GetAssetHistory(start, end int64) ([]AssetSnapshot, error) {
	if sm.assetDataDir == "" {
		return nil, fmt.Errorf("asset data directory not configured")
	}

	// 确定需要读取的日期范围
	startTime := time.Unix(start, 0)
	endTime := time.Unix(end, 0)

	// 使用 UTC+8 (与写入时一致)
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}

	startDay := startTime.In(loc)
	endDay := endTime.In(loc)

	var result []AssetSnapshot

	// 遍历每一天
	for d := startDay; d.Before(endDay) || d.Format("2006-01-02") == endDay.Format("2006-01-02"); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		filename := filepath.Join(sm.assetDataDir, fmt.Sprintf("%s.json", dateStr))

		if _, err := os.Stat(filename); os.IsNotExist(err) {
			continue
		}

		data, err := ioutil.ReadFile(filename)
		if err != nil {
			sm.logger.Warnf("Failed to read asset history file %s: %v", filename, err)
			continue
		}

		var dailySnapshots []AssetSnapshot
		if err := json.Unmarshal(data, &dailySnapshots); err != nil {
			sm.logger.Warnf("Failed to unmarshal asset history file %s: %v", filename, err)
			continue
		}

		// 过滤时间范围
		for _, s := range dailySnapshots {
			if s.Timestamp >= start && s.Timestamp <= end {
				result = append(result, s)
			}
		}
	}

	// 按时间排序 (虽然文件顺序应该是对的，但保险起见)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp < result[j].Timestamp
	})

	// 降采样 (如果数据量太大)
	// 例如，如果请求范围超过 7 天，每 10 分钟取一个点
	// 这里简单起见，如果点数超过 2000，均匀抽样
	if len(result) > 2000 {
		step := len(result) / 2000
		var sampled []AssetSnapshot
		for i := 0; i < len(result); i += step {
			sampled = append(sampled, result[i])
		}
		// 确保最后一个点被包含
		if result[len(result)-1].Timestamp != sampled[len(sampled)-1].Timestamp {
			sampled = append(sampled, result[len(result)-1])
		}
		result = sampled
	}

	return result, nil
}

// GetStatisticsResponse 获取格式化的统计数据响应
func (sm *StatisticsManager) GetStatisticsResponse(symbol string) *StatisticsResponse {
	stats := sm.GetSymbolStatistics(symbol)
	if stats == nil {
		return nil
	}

	response := &StatisticsResponse{
		Symbol: symbol,
	}

	stats.mu.RLock()
	defer stats.mu.RUnlock()

	// 1. 滑点统计
	if stats.SlippageStats != nil {
		response.SlippageStats = map[string]map[string]float64{
			// 新的 A/B 键
			"aBuy": {
				"average":      stats.SlippageStats.ABuy.CalculateAverage(),
				"max":          stats.SlippageStats.ABuy.CalculateMax(),
				"min":          stats.SlippageStats.ABuy.CalculateMin(),
				"percentile90": stats.SlippageStats.ABuy.CalculatePercentile90(),
			},
			"aSell": {
				"average":      stats.SlippageStats.ASell.CalculateAverage(),
				"max":          stats.SlippageStats.ASell.CalculateMax(),
				"min":          stats.SlippageStats.ASell.CalculateMin(),
				"percentile90": stats.SlippageStats.ASell.CalculatePercentile90(),
			},
			"bBuy": {
				"average":      stats.SlippageStats.BBuy.CalculateAverage(),
				"max":          stats.SlippageStats.BBuy.CalculateMax(),
				"min":          stats.SlippageStats.BBuy.CalculateMin(),
				"percentile90": stats.SlippageStats.BBuy.CalculatePercentile90(),
			},
			"bSell": {
				"average":      stats.SlippageStats.BSell.CalculateAverage(),
				"max":          stats.SlippageStats.BSell.CalculateMax(),
				"min":          stats.SlippageStats.BSell.CalculateMin(),
				"percentile90": stats.SlippageStats.BSell.CalculatePercentile90(),
			},
			// 向后兼容：保留旧的键
			"exchangeBuy": {
				"average":      stats.SlippageStats.ExchangeBuy.CalculateAverage(),
				"max":          stats.SlippageStats.ExchangeBuy.CalculateMax(),
				"min":          stats.SlippageStats.ExchangeBuy.CalculateMin(),
				"percentile90": stats.SlippageStats.ExchangeBuy.CalculatePercentile90(),
			},
			"exchangeSell": {
				"average":      stats.SlippageStats.ExchangeSell.CalculateAverage(),
				"max":          stats.SlippageStats.ExchangeSell.CalculateMax(),
				"min":          stats.SlippageStats.ExchangeSell.CalculateMin(),
				"percentile90": stats.SlippageStats.ExchangeSell.CalculatePercentile90(),
			},
			"onchainBuy": {
				"average":      stats.SlippageStats.OnChainBuy.CalculateAverage(),
				"max":          stats.SlippageStats.OnChainBuy.CalculateMax(),
				"min":          stats.SlippageStats.OnChainBuy.CalculateMin(),
				"percentile90": stats.SlippageStats.OnChainBuy.CalculatePercentile90(),
			},
			"onchainSell": {
				"average":      stats.SlippageStats.OnChainSell.CalculateAverage(),
				"max":          stats.SlippageStats.OnChainSell.CalculateMax(),
				"min":          stats.SlippageStats.OnChainSell.CalculateMin(),
				"percentile90": stats.SlippageStats.OnChainSell.CalculatePercentile90(),
			},
		}
	}

	// 2. 成本统计
	if stats.CostStats != nil {
		response.CostStats = map[string]map[string]float64{
			"costInCoin": {
				"average":      stats.CostStats.CostInCoin.CalculateAverage(),
				"max":          stats.CostStats.CostInCoin.CalculateMax(),
				"min":          stats.CostStats.CostInCoin.CalculateMin(),
				"percentile90": stats.CostStats.CostInCoin.CalculatePercentile90(),
			},
			"costPercent": {
				"average":      stats.CostStats.CostPercent.CalculateAverage(),
				"max":          stats.CostStats.CostPercent.CalculateMax(),
				"min":          stats.CostStats.CostPercent.CalculateMin(),
				"percentile90": stats.CostStats.CostPercent.CalculatePercentile90(),
			},
		}
	}

	// 3. Size统计
	if stats.SizeStats != nil {
		response.SizeStats = map[string]map[string]float64{
			"size": {
				"average":      stats.SizeStats.Size.CalculateAverage(),
				"max":          stats.SizeStats.Size.CalculateMax(),
				"min":          stats.SizeStats.Size.CalculateMin(),
				"percentile90": stats.SizeStats.Size.CalculatePercentile90(),
			},
			"sizeUSDT": {
				"average":      stats.SizeStats.SizeUSDT.CalculateAverage(),
				"max":          stats.SizeStats.SizeUSDT.CalculateMax(),
				"min":          stats.SizeStats.SizeUSDT.CalculateMin(),
				"percentile90": stats.SizeStats.SizeUSDT.CalculatePercentile90(),
			},
		}
	}

	// 4. Trigger统计
	if stats.TriggerStats != nil {
		stats.TriggerStats.mu.RLock()
		response.TriggerStats = map[string]interface{}{
			"totalTrades":     stats.TriggerStats.TotalTrades,
			"totalVolume":     stats.TriggerStats.TotalVolume,
			"totalVolumeUSDT": stats.TriggerStats.TotalVolumeUSDT,
			"totalProfit":     stats.TriggerStats.TotalProfit,
			"winCount":        stats.TriggerStats.WinCount,
			"lossCount":       stats.TriggerStats.LossCount,
		}
		stats.TriggerStats.mu.RUnlock()
	}

	// 5. 时间序列数据
	// 成交数据
	if stats.TradeRecords != nil {
		response.TradeRecords = make([]map[string]interface{}, 0, len(stats.TradeRecords))
		for _, trade := range stats.TradeRecords {
			response.TradeRecords = append(response.TradeRecords, map[string]interface{}{
				"timestamp":   trade.Timestamp.Unix(),
				"direction":   trade.Direction,
				"size":        trade.Size,
				"sizeUSDT":    trade.SizeUSDT,
				"price":       trade.Price,
				"diffValue":   trade.DiffValue,
				"profit":      trade.Profit,
				"costInCoin":  trade.CostInCoin,
				"costPercent": trade.CostPercent,
			})
		}
	}

	// 价差数据
	if stats.PriceDiffRecords != nil {
		response.PriceDiffRecords = make([]map[string]interface{}, 0, len(stats.PriceDiffRecords))
		for _, record := range stats.PriceDiffRecords {
			response.PriceDiffRecords = append(response.PriceDiffRecords, map[string]interface{}{
				"timestamp": record.Timestamp.Unix(),
				"diffAB":    record.DiffAB,
				"diffBA":    record.DiffBA,
			})
		}
	}

	// 价格数据
	if stats.PriceRecords != nil {
		response.PriceRecords = make([]map[string]interface{}, 0, len(stats.PriceRecords))
		for _, record := range stats.PriceRecords {
			response.PriceRecords = append(response.PriceRecords, map[string]interface{}{
				"timestamp": record.Timestamp.Unix(),
				// 保留原字段名（向后兼容）
				"exchangeBid": record.ExchangeBid,
				"exchangeAsk": record.ExchangeAsk,
				"onchainBid":  record.OnchainBid,
				"onchainAsk":  record.OnchainAsk,
				// 添加更直观的字段名
				"exchange": map[string]interface{}{
					"bid":       record.ExchangeBid, // 交易所买一价（我们卖给交易所的价格）
					"ask":       record.ExchangeAsk, // 交易所卖一价（我们从交易所买的价格）
					"sellPrice": record.ExchangeBid, // 我们在交易所的卖出价
					"buyPrice":  record.ExchangeAsk, // 我们在交易所的买入价
				},
				"onchain": map[string]interface{}{
					"bid":       record.OnchainBid, // 链上买一价（我们卖给DEX的价格）
					"ask":       record.OnchainAsk, // 链上卖一价（我们从DEX买的价格）
					"sellPrice": record.OnchainBid, // 我们在链上的卖出价
					"buyPrice":  record.OnchainAsk, // 我们在链上的买入价
				},
			})
		}
	}

	return response
}

// 辅助函数：创建统计结构

func newSlippageStatistics() *SlippageStatistics {
	return &SlippageStatistics{
		ExchangeBuy:  NewStatisticValues(),
		ExchangeSell: NewStatisticValues(),
		OnChainBuy:   NewStatisticValues(),
		OnChainSell:  NewStatisticValues(),
		ABuy:         NewStatisticValues(),
		ASell:        NewStatisticValues(),
		BBuy:         NewStatisticValues(),
		BSell:        NewStatisticValues(),
	}
}

func newCostStatistics() *CostStatistics {
	return &CostStatistics{
		CostInCoin:  NewStatisticValues(),
		CostPercent: NewStatisticValues(),
	}
}

func newSizeStatistics() *SizeStatistics {
	return &SizeStatistics{
		Size:     NewStatisticValues(),
		SizeUSDT: NewStatisticValues(),
	}
}

func newTriggerStatistics() *TriggerStatistics {
	return &TriggerStatistics{
		TotalTrades:     0,
		TotalVolume:     0,
		TotalVolumeUSDT: 0,
		TotalProfit:     0,
		WinCount:        0,
		LossCount:       0,
	}
}

// StatisticValues 方法

// NewStatisticValues 创建新的统计值集合
func NewStatisticValues() *StatisticValues {
	return &StatisticValues{
		Values: make([]float64, 0),
	}
}

// AddValue 添加值
func (sv *StatisticValues) AddValue(value float64) {
	sv.mu.Lock()
	defer sv.mu.Unlock()

	sv.Values = append(sv.Values, value)

	// 限制长度（保留最近的数据）
	maxValues := 10000
	if len(sv.Values) > maxValues {
		sv.Values = sv.Values[len(sv.Values)-maxValues:]
	}
}

// GetValues 获取所有值（副本）
func (sv *StatisticValues) GetValues() []float64 {
	sv.mu.RLock()
	defer sv.mu.RUnlock()

	result := make([]float64, len(sv.Values))
	copy(result, sv.Values)
	return result
}

// CalculateAverage 计算平均值
func (sv *StatisticValues) CalculateAverage() float64 {
	values := sv.GetValues()
	if len(values) == 0 {
		return 0
	}

	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// CalculateMax 计算最大值
func (sv *StatisticValues) CalculateMax() float64 {
	values := sv.GetValues()
	if len(values) == 0 {
		return 0
	}

	max := values[0]
	for _, v := range values {
		if v > max {
			max = v
		}
	}
	return max
}

// CalculateMin 计算最小值
func (sv *StatisticValues) CalculateMin() float64 {
	values := sv.GetValues()
	if len(values) == 0 {
		return 0
	}

	min := values[0]
	for _, v := range values {
		if v < min {
			min = v
		}
	}
	return min
}

// CalculatePercentile90 计算90分位值
func (sv *StatisticValues) CalculatePercentile90() float64 {
	values := sv.GetValues()
	if len(values) == 0 {
		return 0
	}

	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	index := int(float64(len(sorted)) * 0.9)
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
