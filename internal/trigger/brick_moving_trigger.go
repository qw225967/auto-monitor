package trigger

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/onchain"
	"auto-arbitrage/internal/onchain/bundler"
	"auto-arbitrage/internal/position"
	"auto-arbitrage/internal/statistics"
	"auto-arbitrage/internal/statistics/monitor"
	"auto-arbitrage/internal/trader"
	"auto-arbitrage/internal/trigger/token_mapping"
	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/utils/parallel"
)

func writeBMDebug(loc, msg, hyp string, data map[string]interface{}) {}

// BrickMovingTrigger 搬砖 trigger，使用手动输入的固定阈值，不使用 analytics 阈值计算
type BrickMovingTrigger struct {
	ID      uint64
	sourceA trader.Trader
	sourceB trader.Trader
	symbol  string

	intervalOpt *IntervalOpt // 循环间隔管理
	slippageOpt *SlippageOpt // 滑点管理
	orderOpt    *OrderOpt    // 下单管理

	// 价格数据通道：分别接收来自 sourceA 和 sourceB 的价格数据
	sourceAPriceChan chan TriggerPriceData
	sourceBPriceChan chan TriggerPriceData

	// 方向配置（使用 DirectionConfig 整合）
	directionAB *DirectionConfig // +A-B 方向配置
	directionBA *DirectionConfig // -A+B 方向配置

	// 链上数据
	onChainData *OnChainData

	lastTicker *model.Ticker

	context context.Context
	close   context.CancelFunc

	// 手动输入的固定阈值（不使用 analytics）
	thresholdAB float64 // +A-B 方向的固定阈值
	thresholdBA float64 // -A+B 方向的固定阈值

	// 搬砖区 Web 配置的固定 size，按方向区分，下单与链上 swap 使用对应方向的值
	configuredSizeAB float64 // +A-B 方向
	configuredSizeBA float64 // -A+B 方向

	backgroundTaskRoutineGroup *parallel.RoutineGroup
	priceConsumerRoutineGroup  *parallel.RoutineGroup
	logger                     *zap.SugaredLogger

	// 状态管理
	isRunning bool
	statusMu  sync.RWMutex

	// 本 trigger 最新价差（与 statistics 按 symbol 区分，同 symbol 多 trigger 时列表各显各的）
	lastDiffAB  float64
	lastDiffBA  float64
	lastDiffSet bool
	lastDiffMu  sync.RWMutex

	// 执行中锁：防止同一方向并发执行（冷却在 executeOrderV2 返回后才更新，执行期间多个 tick 可能同时通过冷却检查）
	executingAB bool
	executingBA bool
	executingMu sync.Mutex

	// Telegram 通知配置
	telegramNotificationEnabled bool
	telegramNotificationMu      sync.RWMutex

	// 类型信息
	traderAType string
	traderBType string

	// 链上配置（Source A / B 独立）
	onChainConfigMu  sync.RWMutex
	onChainSlippageA string
	onChainSlippageB string
	gasMultiplierA   float64
	gasMultiplierB   float64
	onChainGasLimitA string
	onChainGasLimitB string

	// 交易完成回调：direction 为 "AB" 或 "BA"，由外部（如 Dashboard）注册以触发 Pipeline 联动
	onTradeCompleteFn func(symbol string, direction string)
	onTradeCompleteMu sync.RWMutex

	// 价差更新回调：diffAB、diffBA 更新时调用，供智能翻转充提决策
	onSpreadUpdateFn func(symbol string, triggerID uint64, diffAB, diffBA float64)
	onSpreadUpdateMu sync.RWMutex

	// 按方向分开的腿状态，供 Web 展示：+A-B 与 -A+B 可能同时操作
	tradeStatusAB_A string
	tradeStatusAB_B string
	tradeStatusBA_B string
	tradeStatusBA_A string
	tradeStatusMu   sync.RWMutex
}

// SetOnTradeComplete 注册交易完成回调
func (t *BrickMovingTrigger) SetOnTradeComplete(fn func(symbol string, direction string)) {
	t.onTradeCompleteMu.Lock()
	defer t.onTradeCompleteMu.Unlock()
	t.onTradeCompleteFn = fn
}

// SetOnSpreadUpdate 注册价差更新回调（供智能翻转充提决策）
func (t *BrickMovingTrigger) SetOnSpreadUpdate(fn func(symbol string, triggerID uint64, diffAB, diffBA float64)) {
	t.onSpreadUpdateMu.Lock()
	defer t.onSpreadUpdateMu.Unlock()
	t.onSpreadUpdateFn = fn
}

func (t *BrickMovingTrigger) notifyTradeComplete(direction string) {
	t.onTradeCompleteMu.RLock()
	fn := t.onTradeCompleteFn
	t.onTradeCompleteMu.RUnlock()
	if fn != nil {
		go fn(t.symbol, direction)
	}
}

// GetTradeStatusAB 返回 +A-B 方向的 A/B 腿状态，如 "Apending"/"A完成"，无执行时为空
func (t *BrickMovingTrigger) GetTradeStatusAB() (statusA, statusB string) {
	t.tradeStatusMu.RLock()
	defer t.tradeStatusMu.RUnlock()
	return t.tradeStatusAB_A, t.tradeStatusAB_B
}

// GetTradeStatusBA 返回 -A+B 方向的 B/A 腿状态（先 B 后 A），如 "Bpending"/"B完成"，无执行时为空
func (t *BrickMovingTrigger) GetTradeStatusBA() (statusB, statusA string) {
	t.tradeStatusMu.RLock()
	defer t.tradeStatusMu.RUnlock()
	return t.tradeStatusBA_B, t.tradeStatusBA_A
}

func (t *BrickMovingTrigger) setTradeStatusAB(legA, legB string) {
	t.tradeStatusMu.Lock()
	t.tradeStatusAB_A, t.tradeStatusAB_B = legA, legB
	t.tradeStatusMu.Unlock()
}

func (t *BrickMovingTrigger) setTradeStatusBA(legB, legA string) {
	t.tradeStatusMu.Lock()
	t.tradeStatusBA_B, t.tradeStatusBA_A = legB, legA
	t.tradeStatusMu.Unlock()
}

// scheduleResetTradeStatus 交易完成后延迟重置该方向状态，避免一直显示旧结果
func (t *BrickMovingTrigger) scheduleResetTradeStatus(direction OrderDirection) {
	go func() {
		time.Sleep(2 * time.Second)
		if direction == DirectionAB {
			t.setTradeStatusAB("", "")
		} else {
			t.setTradeStatusBA("", "")
		}
	}()
}

// GetOrderLoopInterval 返回尝试交易循环间隔（下单 loop 周期）
func (t *BrickMovingTrigger) GetOrderLoopInterval() time.Duration {
	t.statusMu.RLock()
	defer t.statusMu.RUnlock()
	if t.intervalOpt != nil {
		return t.intervalOpt.orderLoop
	}
	return 500 * time.Millisecond
}

// SetOrderLoopInterval 设置尝试交易循环间隔
func (t *BrickMovingTrigger) SetOrderLoopInterval(d time.Duration) {
	t.statusMu.Lock()
	defer t.statusMu.Unlock()
	if t.intervalOpt != nil {
		t.intervalOpt.orderLoop = d
	}
}

// GetCooldown 返回同方向两次下单的冷却时间
func (t *BrickMovingTrigger) GetCooldown() time.Duration {
	t.statusMu.RLock()
	defer t.statusMu.RUnlock()
	if t.orderOpt != nil {
		return t.orderOpt.cooldown
	}
	return 2 * time.Second
}

// SetCooldown 设置同方向两次下单的冷却时间
func (t *BrickMovingTrigger) SetCooldown(d time.Duration) {
	t.statusMu.Lock()
	defer t.statusMu.Unlock()
	if t.orderOpt != nil {
		t.orderOpt.cooldown = d
	}
}

// NewBrickMovingTrigger 创建新的搬砖 trigger
func NewBrickMovingTrigger(
	id uint64,
	symbol string,
	sourceA, sourceB trader.Trader,
	traderAType, traderBType string,
	thresholdAB, thresholdBA float64,
) *BrickMovingTrigger {
	ctx, cancel := context.WithCancel(context.Background())
	log := logger.GetLoggerInstance().Named("BrickMovingTrigger").Sugar()

	intervalOpt := &IntervalOpt{
		orderLoop: 500 * time.Millisecond,
	}

	slippageOpt := &SlippageOpt{
		limit:  0.5, // 默认 0.5%
		amount: 0,
	}

	orderOpt := &OrderOpt{
		cooldown: 2 * time.Second,
	}

	return &BrickMovingTrigger{
		ID:      id,
		sourceA: sourceA,
		sourceB: sourceB,
		symbol:  symbol,

		intervalOpt: intervalOpt,
		slippageOpt: slippageOpt,
		orderOpt:    orderOpt,

		sourceAPriceChan: make(chan TriggerPriceData, 100),
		sourceBPriceChan: make(chan TriggerPriceData, 100),

		directionAB: &DirectionConfig{
			Direction:             DirectionAB,
			PriceData:             model.PriceData{},
			LastOrderTime:         time.Time{},
			OrderExecutionEnabled: false,
		},
		directionBA: &DirectionConfig{
			Direction:             DirectionBA,
			PriceData:             model.PriceData{},
			LastOrderTime:         time.Time{},
			OrderExecutionEnabled: false,
		},

		onChainData: &OnChainData{
			BuyTx:      "",
			SellTx:     "",
			ChainIndex: "",
		},

		context: ctx,
		close:   cancel,

		thresholdAB:      thresholdAB,
		thresholdBA:      thresholdBA,
		configuredSizeAB: 1000.0, // 默认 1000，由 Web 保存配置时更新
		configuredSizeBA: 1000.0,

		backgroundTaskRoutineGroup: parallel.NewRoutineGroup(),
		priceConsumerRoutineGroup:  parallel.NewRoutineGroup(),
		logger:                     log,

		isRunning: false,

		telegramNotificationEnabled: false,

		traderAType: traderAType,
		traderBType: traderBType,
	}
}

// SetThresholdAB 设置 +A-B 方向的阈值
func (t *BrickMovingTrigger) SetThresholdAB(threshold float64) {
	t.statusMu.Lock()
	defer t.statusMu.Unlock()
	t.thresholdAB = threshold
	t.logger.Infof("Set +A-B threshold to %.6f", threshold)
}

// SetThresholdBA 设置 -A+B 方向的阈值
func (t *BrickMovingTrigger) SetThresholdBA(threshold float64) {
	t.statusMu.Lock()
	defer t.statusMu.Unlock()
	t.thresholdBA = threshold
	t.logger.Infof("Set -A+B threshold to %.6f", threshold)
}

// SetConfiguredSizeAB 设置 +A-B 方向的 Web 配置固定 size
func (t *BrickMovingTrigger) SetConfiguredSizeAB(size float64) {
	t.statusMu.Lock()
	defer t.statusMu.Unlock()
	t.configuredSizeAB = size
	t.logger.Infof("Set configured size +A-B to %.6f", size)
}

// SetConfiguredSizeBA 设置 -A+B 方向的 Web 配置固定 size
func (t *BrickMovingTrigger) SetConfiguredSizeBA(size float64) {
	t.statusMu.Lock()
	defer t.statusMu.Unlock()
	t.configuredSizeBA = size
	t.logger.Infof("Set configured size -A+B to %.6f", size)
}

// GetConfiguredSizeAB 获取 +A-B 方向的 Web 配置固定 size
func (t *BrickMovingTrigger) GetConfiguredSizeAB() float64 {
	t.statusMu.RLock()
	defer t.statusMu.RUnlock()
	return t.configuredSizeAB
}

// GetConfiguredSizeBA 获取 -A+B 方向的 Web 配置固定 size
func (t *BrickMovingTrigger) GetConfiguredSizeBA() float64 {
	t.statusMu.RLock()
	defer t.statusMu.RUnlock()
	return t.configuredSizeBA
}

// getConfiguredSizeForDirection 根据方向返回对应配置 size
func (t *BrickMovingTrigger) getConfiguredSizeForDirection(direction OrderDirection) float64 {
	if direction == DirectionAB {
		return t.GetConfiguredSizeAB()
	}
	return t.GetConfiguredSizeBA()
}

// GetThresholdAB 获取 +A-B 方向的阈值
func (t *BrickMovingTrigger) GetThresholdAB() float64 {
	t.statusMu.RLock()
	defer t.statusMu.RUnlock()
	return t.thresholdAB
}

// GetThresholdBA 获取 -A+B 方向的阈值
func (t *BrickMovingTrigger) GetThresholdBA() float64 {
	t.statusMu.RLock()
	defer t.statusMu.RUnlock()
	return t.thresholdBA
}

// SetDirectionEnabledOrder 设置方向的订单执行启用状态
func (t *BrickMovingTrigger) SetDirectionEnabledOrder(direction OrderDirection, enabled bool) {
	t.statusMu.Lock()
	defer t.statusMu.Unlock()
	if direction == DirectionAB {
		t.directionAB.OrderExecutionEnabled = enabled
	} else if direction == DirectionBA {
		t.directionBA.OrderExecutionEnabled = enabled
	}
	t.logger.Infof("Set direction %s OrderExecutionEnabled to %v", direction, enabled)
}

// GetDirectionEnabledOrder 获取方向的订单执行启用状态
func (t *BrickMovingTrigger) GetDirectionEnabledOrder(direction OrderDirection) bool {
	t.statusMu.RLock()
	defer t.statusMu.RUnlock()
	if direction == DirectionAB {
		return t.directionAB.OrderExecutionEnabled
	} else if direction == DirectionBA {
		return t.directionBA.OrderExecutionEnabled
	}
	return false
}

// Start 启动搬砖 trigger（实现 proto.Trigger 接口）
func (t *BrickMovingTrigger) Start(ctx context.Context) error {
	t.statusMu.Lock()
	if t.isRunning {
		t.statusMu.Unlock()
		return fmt.Errorf("trigger %s is already running", t.symbol)
	}

	// 检查 context 是否已被取消，如果是则重新创建
	if t.context != nil && t.context.Err() != nil {
		t.logger.Infof("BrickMovingTrigger %s 的 context 已被取消，正在重新创建...", t.symbol)
		t.context, t.close = context.WithCancel(ctx)
	} else if t.context == nil {
		t.context, t.close = context.WithCancel(ctx)
	}

	// 重新创建后台任务组（如果之前的任务组已经在 Stop() 时等待完成）
	if t.backgroundTaskRoutineGroup == nil {
		t.backgroundTaskRoutineGroup = parallel.NewRoutineGroup()
	}
	if t.priceConsumerRoutineGroup == nil {
		t.priceConsumerRoutineGroup = parallel.NewRoutineGroup()
	}

	t.isRunning = true
	t.statusMu.Unlock()

	// 注册到 PositionManager
	t.registerToPositionManager()

	// 启动价格消费协程
	t.priceConsumerRoutineGroup.GoSafe(func() {
		t.startConsumePriceMsg()
	})

	// 启动下单循环
	parallel.GoSafe(func() {
		t.orderAB(t.context)
	})
	parallel.GoSafe(func() {
		t.orderBA(t.context)
	})

	// 启动价差计算和记录
	parallel.GoSafe(func() {
		t.startCalcPriceDiff()
	})

	t.logger.Infof("BrickMovingTrigger %s started", t.symbol)
	return nil
}

// Stop 停止搬砖 trigger
func (t *BrickMovingTrigger) Stop() error {
	t.statusMu.Lock()
	if !t.isRunning {
		t.statusMu.Unlock()
		return fmt.Errorf("trigger %s is not running", t.symbol)
	}
	t.isRunning = false
	t.statusMu.Unlock()

	// 先停止链上询价循环，避免 goroutine 常驻
	if onchainTrader := t.getOnchainTrader(); onchainTrader != nil {
		onchainTrader.StopSwap()
	}

	// 取消上下文
	t.close()

	// 等待所有协程结束
	t.backgroundTaskRoutineGroup.Wait()
	t.priceConsumerRoutineGroup.Wait()

	// 从 PositionManager 取消注册
	t.unregisterFromPositionManager()

	t.logger.Infof("BrickMovingTrigger %s stopped", t.symbol)
	return nil
}

// IsRunning 检查 trigger 是否正在运行
func (t *BrickMovingTrigger) IsRunning() bool {
	t.statusMu.RLock()
	defer t.statusMu.RUnlock()
	return t.isRunning
}

// registerToPositionManager 注册到 PositionManager
func (t *BrickMovingTrigger) registerToPositionManager() {
	positionManager := position.GetPositionManager()
	if positionManager == nil {
		t.logger.Warn("PositionManager 未初始化，跳过注册")
		return
	}

	positionManager.RegisterSymbol(t.symbol)

	if onchainTrader := t.getOnchainTrader(); onchainTrader != nil {
		positionManager.RegisterOnchainTrader(t.symbol, onchainTrader)
	}

	positionManager.RegisterTraderTypes(t.symbol, t.traderAType, t.traderBType)
	positionManager.TriggerImmediateUpdate(t.symbol)

	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager != nil {
		statisticsManager.RegisterSymbol(t.symbol)
	}
}

// unregisterFromPositionManager 从 PositionManager 取消注册
func (t *BrickMovingTrigger) unregisterFromPositionManager() {
	positionManager := position.GetPositionManager()
	if positionManager != nil {
		positionManager.UnregisterOnchainTrader(t.symbol)
		positionManager.UnregisterSymbol(t.symbol)
		positionManager.UnregisterTraderTypes(t.symbol)
	}

	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager != nil {
		statisticsManager.UnregisterSymbol(t.symbol)
	}
}

// getOnchainTrader 获取 OnchainTrader（从 sourceA 或 sourceB）
func (t *BrickMovingTrigger) getOnchainTrader() trader.OnchainTrader {
	if t.sourceA != nil {
		if onchainTrader, ok := t.sourceA.(trader.OnchainTrader); ok {
			return onchainTrader
		}
	}
	if t.sourceB != nil {
		if onchainTrader, ok := t.sourceB.(trader.OnchainTrader); ok {
			return onchainTrader
		}
	}
	return nil
}

// startConsumePriceMsg 启动价格消息消费（复用原有逻辑）
func (t *BrickMovingTrigger) startConsumePriceMsg() {
	// 为 sourceA 和 sourceB 设置价格回调
	t.setupPriceCallbacks()

	// 设置链上订阅（如果 sourceA 或 sourceB 是链上类型）
	t.setupOnchainSubscription()

	// 从 channel 接收价格数据（回调函数会将数据写入这些 channel）
	for {
		select {
		case <-t.context.Done():
			t.logger.Debugf("[价格消费] BrickMovingTrigger %s 的 context 已取消，退出价格消费循环", t.symbol)
			return
		case priceData := <-t.sourceAPriceChan:
			// 处理来自 sourceA 的价格数据
			if t.context.Err() != nil {
				t.logger.Debugf("[价格消费] BrickMovingTrigger %s 的 context 已取消，停止处理价格数据", t.symbol)
				return
			}
			t.handleSourceAPrice(priceData)
		case priceData := <-t.sourceBPriceChan:
			// 处理来自 sourceB 的价格数据
			if t.context.Err() != nil {
				t.logger.Debugf("[价格消费] BrickMovingTrigger %s 的 context 已取消，停止处理价格数据", t.symbol)
				return
			}
			t.handleSourceBPrice(priceData)
		}
	}
}

// setupPriceCallbacks 为 sourceA 和 sourceB 设置价格回调
// 仅对 OnchainTrader 调用 SetPriceCallback；CEX/DEX 的价格由 handlePriceMsgChan 经 exchangePriceMsgChan 按 exchangeType 路由到 sourceXPriceChan
func (t *BrickMovingTrigger) setupPriceCallbacks() {
	onchainCallback := func(targetChan chan TriggerPriceData, logLabel string) trader.PriceCallback {
		return func(symbol string, priceData trader.PriceData) {
			if priceData.Ticker != nil {
				if symbol != t.symbol {
					return
				}
				select {
				case targetChan <- TriggerPriceData{ExchangePriceMsg: ExchangePriceMsg{symbol: symbol, ticker: priceData.Ticker}}:
				default:
					t.logger.Warnf("%s price channel full, dropping ticker", logLabel)
				}
				return
			}
			if priceData.ChainPrice != nil {
				p := priceData.ChainPrice
				if p.ChainBuyTx != "" {
					t.onChainData.BuyTx = p.ChainBuyTx
				}
				if p.ChainSellTx != "" {
					t.onChainData.SellTx = p.ChainSellTx
				}
				if p.ChainId != "" {
					t.onChainData.ChainIndex = p.ChainId
				}
				select {
				case targetChan <- TriggerPriceData{OnChainPriceMsg: OnChainPriceMsg{price: p}}:
				default:
					// 缓冲满时先腾出一格再试一次，尽量把最新链上价写入
					select {
					case <-targetChan:
					default:
					}
					select {
					case targetChan <- TriggerPriceData{OnChainPriceMsg: OnChainPriceMsg{price: p}}:
					default:
						t.logger.Warnf("%s price channel full, dropping onchain price", logLabel)
					}
				}
			}
		}
	}

	if t.sourceA != nil {
		if _, ok := t.sourceA.(trader.OnchainTrader); ok {
			t.sourceA.SetPriceCallback(onchainCallback(t.sourceAPriceChan, "SourceA"))
			t.logger.Debugf("Set price callback for sourceA (onchain, type: %s)", t.sourceA.GetType())
		}
	}
	if t.sourceB != nil {
		if _, ok := t.sourceB.(trader.OnchainTrader); ok {
			t.sourceB.SetPriceCallback(onchainCallback(t.sourceBPriceChan, "SourceB"))
			t.logger.Debugf("Set price callback for sourceB (onchain, type: %s)", t.sourceB.GetType())
		}
	}
}

// setupOnchainSubscription 为链上的 sourceA 或 sourceB 设置 swapInfo 并启动 swap
func (t *BrickMovingTrigger) setupOnchainSubscription() {
	// 处理 sourceA 的链上订阅
	if t.sourceA != nil {
		if onchainTrader, ok := t.sourceA.(trader.OnchainTrader); ok {
			if err := t.setupOnchainForTrader(onchainTrader, "sourceA", t.traderAType); err != nil {
				t.logger.Errorf("Failed to setup onchain subscription for sourceA: %v", err)
			}
		}
	}

	// 处理 sourceB 的链上订阅
	if t.sourceB != nil {
		if onchainTrader, ok := t.sourceB.(trader.OnchainTrader); ok {
			if err := t.setupOnchainForTrader(onchainTrader, "sourceB", t.traderBType); err != nil {
				t.logger.Errorf("Failed to setup onchain subscription for sourceB: %v", err)
			}
		}
	}
}

// setupOnchainForTrader 为指定的 OnchainTrader 设置 swapInfo 并启动 swap
func (t *BrickMovingTrigger) setupOnchainForTrader(onchainTrader trader.OnchainTrader, sourceName string, traderType string) error {
	// 类型断言为 *OnchainTraderImpl 以访问 GetOnchainClient 方法
	onchainTraderImpl, ok := onchainTrader.(*trader.OnchainTraderImpl)
	if !ok {
		return fmt.Errorf("failed to cast onchainTrader to *OnchainTraderImpl for %s", sourceName)
	}

	// 获取 OnchainClient 以设置 bundler
	onchainClient := onchainTraderImpl.GetOnchainClient()
	if onchainClient == nil {
		return fmt.Errorf("onchain client is nil for %s", sourceName)
	}

	// 设置 bundler（如果配置了）
	if config.GetGlobalConfig().Bundler.UseBundler {
		bundlerMgr := t.setupBundler()
		if bundlerMgr != nil {
			onchain.SetBundlerForClient(onchainClient, bundlerMgr, true)
			t.logger.Infof("Bundler enabled for %s onchain trader (bundlers: %d)", sourceName, len(bundlerMgr.GetAllBundlers()))
		} else {
			t.logger.Warnf("Bundler is enabled but no bundler configured (check FlashbotsPrivateKey or FortyEightClubAPIKey)")
		}
	}

	// 从 symbol 中提取目标代币符号（例如：从 "RAVEUSDT" 提取 "RAVE"）
	toTokenSymbol := strings.TrimSuffix(t.symbol, "USDT")
	if toTokenSymbol == t.symbol {
		// 如果没有 USDT 后缀，尝试其他常见后缀
		toTokenSymbol = strings.TrimSuffix(t.symbol, "USDC")
		if toTokenSymbol == t.symbol {
			toTokenSymbol = strings.TrimSuffix(t.symbol, "BUSD")
		}
	}

	// 获取 TokenMappingManager
	mappingMgr := token_mapping.GetTokenMappingManager()

	// 默认 USDT 地址（BSC）
	fromTokenSymbol := "USDT"
	fromTokenAddress := "0x55d398326f99059ff775485246999027b3197955"
	fromTokenDecimals := "18"

	// 获取链ID，优先使用 onChainData.ChainIndex，如果未设置则从 traderType 解析，最后使用默认值 "56"
	chainId := "56" // 默认 BSC 链
	if t.onChainData != nil && t.onChainData.ChainIndex != "" {
		chainId = t.onChainData.ChainIndex
	} else if traderType != "" && strings.HasPrefix(traderType, "onchain:") {
		// 从 traderType 解析链ID（如 "onchain:56" -> "56"）
		parts := strings.Split(traderType, ":")
		if len(parts) == 2 {
			chainId = parts[1]
		}
	}

	// 按链从映射中获取 USDT 地址（如果存在）
	if usdtAddr, err := mappingMgr.GetAddressBySymbol("USDT", chainId); err == nil {
		fromTokenAddress = usdtAddr
		t.logger.Debugf("从映射中获取 USDT 地址(chain %s): %s", chainId, usdtAddr)
	}

	// 从映射中获取目标代币在指定链上的合约地址
	toTokenAddress := ""
	toTokenDecimals := "18" // 默认精度

	addr, err := mappingMgr.GetAddressBySymbol(toTokenSymbol, chainId)
	if err != nil {
		errMsg := fmt.Sprintf("无法获取 %s 在链 %s 的合约地址映射，请先在 Token 映射管理中添加映射或执行扫链。错误: %v", toTokenSymbol, chainId, err)
		t.logger.Errorf(errMsg)
		return fmt.Errorf("%s", errMsg)
	}
	toTokenAddress = addr
	t.logger.Infof("从映射中获取 %s 地址(chain %s): %s", toTokenSymbol, chainId, addr)

	// 链上 swap 初始 amount 使用 +A-B 方向 size（运行时按执行方向会更新）
	defaultAmount := t.GetConfiguredSizeAB()
	if defaultAmount <= 0 {
		defaultAmount = 1000.0
	}
	amountStr := fmt.Sprintf("%.0f", defaultAmount)
	// 滑点与 GasLimit：优先使用本 trigger 的 A/B 配置，否则用全局默认
	slippage := t.getOnChainSlippageForSource(sourceName)
	gasLimit := t.getOnChainGasLimitForSource(sourceName)
	if gasLimit == "" || gasLimit == "0" {
		gasLimit = config.GetGlobalConfig().Onchain.DefaultGasLimit
		if gasLimit == "" {
			gasLimit = "500000"
		}
	}

	swapInfo := &model.SwapInfo{
		FromTokenSymbol:          fromTokenSymbol,
		ToTokenSymbol:            toTokenSymbol,
		FromTokenContractAddress: fromTokenAddress,
		ToTokenContractAddress:   toTokenAddress,
		ChainIndex:               chainId,
		Amount:                   amountStr,
		DecimalsFrom:             fromTokenDecimals,
		DecimalsTo:               toTokenDecimals,
		SwapMode:                 "exactIn",
		Slippage:                 slippage,
		GasLimit:                 gasLimit,
		WalletAddress:            config.GetGlobalConfig().Wallet.WalletAddress,
	}

	t.logger.Infof("初始化链上订阅 [%s]: %s -> %s (%s -> %s)", sourceName, fromTokenSymbol, toTokenSymbol, fromTokenAddress, toTokenAddress)

	// 设置 swapInfo 并启动 swap
	// StartSwap 内部会自动设置 swapInfo，所以不需要先调用 SetSwapInfo
	onchainTrader.StartSwap(swapInfo)
	// 应用 gas 乘数（不保存在 SwapInfo，需单独设置）
	if onchainClient := onchainTraderImpl.GetOnchainClient(); onchainClient != nil {
		onchainClient.SetGasMultiplier(t.getOnChainGasMultiplierForSource(sourceName))
	}

	// 注册到 PositionManager（如果还未注册）
	positionManager := position.GetPositionManager()
	if positionManager != nil {
		positionManager.RegisterOnchainTrader(t.symbol, onchainTrader)
		// 注册 trader 类型，供 TriggerImmediateUpdate/余额刷新等使用，避免 "trader type is empty"
		positionManager.RegisterTraderTypes(t.symbol, t.traderAType, t.traderBType)
		t.logger.Infof("已注册 OnchainTrader 到 PositionManager: symbol=%s, source=%s", t.symbol, sourceName)

		// 立即触发一次 swapInfo.Amount 更新
		positionManager.TriggerImmediateUpdate(t.symbol)
		t.logger.Debugf("已触发立即更新 swapInfo.Amount: symbol=%s", t.symbol)
	} else {
		t.logger.Warnf("PositionManager 未初始化，无法注册 OnchainTrader: symbol=%s, source=%s", t.symbol, sourceName)
	}

	return nil
}

// setupBundler 设置 bundler（如果配置了的话）
func (t *BrickMovingTrigger) setupBundler() *bundler.Manager {
	bundlerMgr := bundler.NewManager()

	// 添加 Flashbots bundler（如果配置了）
	if config.GetGlobalConfig().Bundler.FlashbotsPrivateKey != "" {
		if fb, err := bundler.NewFlashbotsBundler(config.GetGlobalConfig().Bundler.FlashbotsPrivateKey, ""); err == nil {
			bundlerMgr.AddBundler(fb)
		}
	}

	// 添加 48club bundler（如果配置了）
	if fb48, err := bundler.NewFortyEightClubBundler(config.GetGlobalConfig().Bundler.FortyEightClubAPIKey, "", config.GetGlobalConfig().Bundler.FortyEightSoulPointPrivateKey); err == nil {
		bundlerMgr.AddBundler(fb48)
	}

	// 如果没有配置任何 bundler，返回 nil
	if len(bundlerMgr.GetAllBundlers()) == 0 {
		return nil
	}

	return bundlerMgr
}

// handleSourceAPrice 处理来自 sourceA 的价格数据
func (t *BrickMovingTrigger) handleSourceAPrice(priceData TriggerPriceData) {
	t.statusMu.Lock()
	defer t.statusMu.Unlock()

	if priceData.ExchangePriceMsg.ticker != nil {
		// sourceA 是交易所，设置 A 的价格（用于 directionAB 的 AskPrice 和 directionBA 的 BidPrice）
		t.directionAB.PriceData.AskPrice = priceData.ExchangePriceMsg.ticker.AskPrice
		t.directionBA.PriceData.BidPrice = priceData.ExchangePriceMsg.ticker.BidPrice
		t.lastTicker = priceData.ExchangePriceMsg.ticker
		t.updatePriceCacheToPositionManager()
		t.recordPriceToStatistics()
		t.logger.Debugf("Updated price from sourceA (exchange): Bid=%.6f, Ask=%.6f",
			priceData.ExchangePriceMsg.ticker.BidPrice, priceData.ExchangePriceMsg.ticker.AskPrice)
	} else if priceData.OnChainPriceMsg.price != nil {
		// sourceA 是链上，设置 A 的价格（用于 directionAB 的 AskPrice 和 directionBA 的 BidPrice）
		ask, _ := strconv.ParseFloat(priceData.OnChainPriceMsg.price.ChainPriceSell, 64)
		bid, _ := strconv.ParseFloat(priceData.OnChainPriceMsg.price.ChainPriceBuy, 64)
		t.directionAB.PriceData.AskPrice = ask
		t.directionBA.PriceData.BidPrice = bid
		t.updatePriceCacheToPositionManager()
		t.recordPriceToStatistics()
		t.logger.Debugf("Updated price from sourceA (onchain): Bid=%.6f, Ask=%.6f", bid, ask)
	}
}

// handleSourceBPrice 处理来自 sourceB 的价格数据
func (t *BrickMovingTrigger) handleSourceBPrice(priceData TriggerPriceData) {
	t.statusMu.Lock()
	defer t.statusMu.Unlock()

	if priceData.ExchangePriceMsg.ticker != nil {
		// sourceB 是交易所，设置 B 的价格（用于 directionAB 的 BidPrice 和 directionBA 的 AskPrice）
		t.directionAB.PriceData.BidPrice = priceData.ExchangePriceMsg.ticker.BidPrice
		t.directionBA.PriceData.AskPrice = priceData.ExchangePriceMsg.ticker.AskPrice
		t.lastTicker = priceData.ExchangePriceMsg.ticker
		t.updatePriceCacheToPositionManager()
		t.recordPriceToStatistics()
		t.logger.Debugf("Updated price from sourceB (exchange): Bid=%.6f, Ask=%.6f",
			priceData.ExchangePriceMsg.ticker.BidPrice, priceData.ExchangePriceMsg.ticker.AskPrice)
	} else if priceData.OnChainPriceMsg.price != nil {
		// sourceB 是链上，设置 B 的价格（用于 directionAB 的 BidPrice 和 directionBA 的 AskPrice）
		ask, _ := strconv.ParseFloat(priceData.OnChainPriceMsg.price.ChainPriceSell, 64)
		bid, _ := strconv.ParseFloat(priceData.OnChainPriceMsg.price.ChainPriceBuy, 64)
		t.directionAB.PriceData.BidPrice = bid
		t.directionBA.PriceData.AskPrice = ask
		t.updatePriceCacheToPositionManager()
		t.recordPriceToStatistics()
		t.logger.Debugf("Updated price from sourceB (onchain): Bid=%.6f, Ask=%.6f", bid, ask)
	}
}

// updatePriceCacheToPositionManager 更新价格缓存到 PositionManager
func (t *BrickMovingTrigger) updatePriceCacheToPositionManager() {
	positionManager := position.GetPositionManager()
	if positionManager == nil {
		return
	}

	// 构建综合价格数据（包含交易所和链上价格）
	priceData := &model.PriceData{
		BidPrice: t.directionAB.PriceData.BidPrice, // 交易所 Bid
		AskPrice: t.directionBA.PriceData.AskPrice, // 交易所 Ask
	}

	// 只有当价格有效时才更新
	if priceData.BidPrice > 0 && priceData.AskPrice > 0 {
		positionManager.UpdatePrice(t.symbol, priceData)
	}
}

// recordPriceToStatistics 记录价格到 StatisticsManager
func (t *BrickMovingTrigger) recordPriceToStatistics() {
	// 计算价差并记录
	if t.directionAB.PriceData.BidPrice > 0 && t.directionAB.PriceData.AskPrice > 0 &&
		t.directionBA.PriceData.BidPrice > 0 && t.directionBA.PriceData.AskPrice > 0 {
		diff, err := CalculateDiff(&t.directionAB.PriceData, &t.directionBA.PriceData)
		if err == nil {
			statisticsManager := statistics.GetStatisticsManager()
			if statisticsManager != nil {
				statisticsManager.RecordPriceDiff(t.symbol, diff.DiffAB, diff.DiffBA)
			}
		}
	}
}

// startCalcPriceDiff 启动价差计算和记录
func (t *BrickMovingTrigger) startCalcPriceDiff() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-t.context.Done():
			return
		case <-ticker.C:
			t.calcAndRecordPriceDiff()
		}
	}
}

// calcAndRecordPriceDiff 计算并记录价差
func (t *BrickMovingTrigger) calcAndRecordPriceDiff() {
	t.statusMu.RLock()
	priceDataAB := t.directionAB.PriceData
	priceDataBA := t.directionBA.PriceData
	t.statusMu.RUnlock()

	diff, err := CalculateDiff(&priceDataAB, &priceDataBA)
	if err != nil {
		t.logger.Debugf("CalculateDiff error: %v", err)
		return
	}

	// 记录到 StatisticsManager（供其他按 symbol 的查询）
	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager != nil {
		statisticsManager.RecordPriceDiff(t.symbol, diff.DiffAB, diff.DiffBA)
	}
	// 本 trigger 本地存一份，列表按 trigger 展示时用
	t.lastDiffMu.Lock()
	t.lastDiffAB = diff.DiffAB
	t.lastDiffBA = diff.DiffBA
	t.lastDiffSet = true
	t.lastDiffMu.Unlock()

	t.onSpreadUpdateMu.RLock()
	fn := t.onSpreadUpdateFn
	t.onSpreadUpdateMu.RUnlock()
	if fn != nil {
		go fn(t.symbol, t.ID, diff.DiffAB, diff.DiffBA)
	}
}

// GetLatestPriceDiff 返回本 trigger 最近一次计算的价差（同 symbol 多 trigger 时列表各显各的）
func (t *BrickMovingTrigger) GetLatestPriceDiff() (diffAB, diffBA float64, exists bool) {
	t.lastDiffMu.RLock()
	defer t.lastDiffMu.RUnlock()
	if !t.lastDiffSet {
		return 0, 0, false
	}
	return t.lastDiffAB, t.lastDiffBA, true
}

// orderAB +A-B 方向的订单触发循环
func (t *BrickMovingTrigger) orderAB(ctx context.Context) {
	ticker := time.NewTicker(t.intervalOpt.orderLoop)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			parallel.RunSafe(func() {
				t.checkAndExecuteOrderAB()
			})
		}
	}
}

// orderBA -A+B 方向的订单触发循环
func (t *BrickMovingTrigger) orderBA(ctx context.Context) {
	ticker := time.NewTicker(t.intervalOpt.orderLoop)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			parallel.RunSafe(func() {
				t.checkAndExecuteOrderBA()
			})
		}
	}
}

// checkAndExecuteOrderAB 检查并执行 +A-B 方向的订单
func (t *BrickMovingTrigger) checkAndExecuteOrderAB() {
	t.executingMu.Lock()
	if t.executingAB {
		t.executingMu.Unlock()
		return // 已有 +A-B 正在执行，跳过，防止链上/交易所不均衡
	}
	t.executingAB = true
	t.executingMu.Unlock()
	defer func() {
		t.executingMu.Lock()
		t.executingAB = false
		t.executingMu.Unlock()
	}()

	t.statusMu.RLock()
	enabled := t.directionAB.OrderExecutionEnabled
	priceDataAB := t.directionAB.PriceData
	priceDataBA := t.directionBA.PriceData
	lastOrderTime := t.directionAB.LastOrderTime
	threshold := t.thresholdAB
	t.statusMu.RUnlock()

	// 构建检查上下文（复用原有 trigger 的检查逻辑）
	diff, err := CalculateDiff(&priceDataAB, &priceDataBA)
	if err != nil {
		// #region agent log
		writeBMDebug("brick_moving_trigger.go:checkAndExecuteOrderAB", "CalculateDiff error", "H5", map[string]interface{}{"err": err.Error(), "symbol": t.symbol})
		// #endregion
		t.logger.Debugf("CalculateDiff error: %v", err)
		return
	}

	ctx := &OrderCheckContext{
		direction:     DirectionAB,
		priceData:     &priceDataAB,
		threshold:     threshold,
		diffValue:     diff.DiffAB,
		lastOrderTime: &lastOrderTime,
		latestTx:      t.onChainData.BuyTx,
		enabled:       enabled,
	}

	result := t.checkAndExecuteOrderV2(ctx)
	if result.ShouldExecute {
		t.statusMu.Lock()
		t.directionAB.LastOrderTime = time.Now()
		t.statusMu.Unlock()
		t.notifyTradeComplete("AB")
		return
	}

	if result.SkipReason != "" {
		t.logger.Debugf("+A-B skip: %s", result.SkipReason)
	}
}

// checkAndExecuteOrderBA 检查并执行 -A+B 方向的订单
func (t *BrickMovingTrigger) checkAndExecuteOrderBA() {
	t.executingMu.Lock()
	if t.executingBA {
		t.executingMu.Unlock()
		return // 已有 -A+B 正在执行，跳过，防止链上/交易所不均衡
	}
	t.executingBA = true
	t.executingMu.Unlock()
	defer func() {
		t.executingMu.Lock()
		t.executingBA = false
		t.executingMu.Unlock()
	}()

	t.statusMu.RLock()
	enabled := t.directionBA.OrderExecutionEnabled
	priceDataBA := t.directionBA.PriceData
	priceDataAB := t.directionAB.PriceData
	lastOrderTime := t.directionBA.LastOrderTime
	threshold := t.thresholdBA
	t.statusMu.RUnlock()

	// 构建检查上下文（复用原有 trigger 的检查逻辑）
	diff, err := CalculateDiff(&priceDataAB, &priceDataBA)
	if err != nil {
		t.logger.Debugf("CalculateDiff error: %v", err)
		return
	}

	ctx := &OrderCheckContext{
		direction:     DirectionBA,
		priceData:     &priceDataBA,
		threshold:     threshold,
		diffValue:     diff.DiffBA,
		lastOrderTime: &lastOrderTime,
		latestTx:      t.onChainData.SellTx,
		enabled:       enabled,
	}

	result := t.checkAndExecuteOrderV2(ctx)
	if result.ShouldExecute {
		t.statusMu.Lock()
		t.directionBA.LastOrderTime = time.Now()
		t.statusMu.Unlock()
		t.notifyTradeComplete("BA")
		return
	}

	if result.SkipReason != "" {
		t.logger.Debugf("-A+B skip: %s", result.SkipReason)
	}
}

// checkAndExecuteOrderV2 统一的订单检查和执行方法（复用原有 trigger 的逻辑）
func (t *BrickMovingTrigger) checkAndExecuteOrderV2(ctx *OrderCheckContext) *CheckResult {
	// #region agent log
	writeBMDebug("brick_moving_trigger.go:checkAndExecuteOrderV2", "entry", "H1", map[string]interface{}{
		"direction": fmt.Sprint(ctx.direction), "diffValue": ctx.diffValue, "threshold": ctx.threshold, "enabled": ctx.enabled,
		"symbol": t.symbol, "latestTxEmpty": ctx.latestTx == "",
	})
	// #endregion
	result := &CheckResult{
		DiffValue: ctx.diffValue,
		Threshold: ctx.threshold,
	}

	// 1. 价格数据验证
	if !t.validatePriceData(ctx.priceData) {
		result.SkipReason = fmt.Sprintf("价格数据无效: Bid=%.6f, Ask=%.6f", ctx.priceData.BidPrice, ctx.priceData.AskPrice)
		// #region agent log
		writeBMDebug("brick_moving_trigger.go:checkAndExecuteOrderV2", "return skip", "H1", map[string]interface{}{"ShouldExecute": false, "SkipReason": result.SkipReason})
		// #endregion
		return result
	}

	// 2. 启用状态检查
	if !ctx.enabled {
		result.SkipReason = fmt.Sprintf("方向已禁用 (%s)", ctx.direction)
		// #region agent log
		writeBMDebug("brick_moving_trigger.go:checkAndExecuteOrderV2", "return skip", "H1", map[string]interface{}{"ShouldExecute": false, "SkipReason": result.SkipReason})
		// #endregion
		t.logger.Debugf("跳过下单 %s: 方向已禁用", ctx.direction)
		return result
	}

	// 3. Trader 验证
	if t.sourceA == nil || t.sourceB == nil {
		result.SkipReason = fmt.Sprintf("trader 不可用 (%s)", ctx.direction)
		// #region agent log
		writeBMDebug("brick_moving_trigger.go:checkAndExecuteOrderV2", "return skip", "H1", map[string]interface{}{"ShouldExecute": false, "SkipReason": result.SkipReason})
		// #endregion
		return result
	}

	// 4. 交易哈希验证（仅在链上交易模式下检查）
	hasOnchainTrader := false
	if ctx.direction == DirectionAB {
		if _, ok := t.sourceA.(trader.OnchainTrader); ok {
			hasOnchainTrader = true
		}
	} else {
		if _, ok := t.sourceA.(trader.OnchainTrader); ok {
			hasOnchainTrader = true
		}
	}
	if hasOnchainTrader {
		if ctx.latestTx == "" {
			result.SkipReason = fmt.Sprintf("最新交易哈希不可用 (%s)", ctx.direction)
			// #region agent log
			writeBMDebug("brick_moving_trigger.go:checkAndExecuteOrderV2", "return skip", "H1", map[string]interface{}{"ShouldExecute": false, "SkipReason": result.SkipReason})
			// #endregion
			return result
		}
		// 4.1 链上滑点保护校验：确保已配置非零滑点，防止无保护执行
		slippageCfg := t.getOnChainSlippageForSource("A")
		if slippageCfg == "" || slippageCfg == "0" {
			result.SkipReason = "链上滑点未配置或为0，请在配置中设置合理的滑点保护值"
			return result
		}
	}

	// 5. 冷却时间检查
	if ctx.lastOrderTime != nil && time.Since(*ctx.lastOrderTime) < t.orderOpt.cooldown {
		remaining := t.orderOpt.cooldown - time.Since(*ctx.lastOrderTime)
		result.SkipReason = fmt.Sprintf("冷却时间未到，剩余: %v", remaining)
		// #region agent log
		writeBMDebug("brick_moving_trigger.go:checkAndExecuteOrderV2", "return skip", "H1", map[string]interface{}{"ShouldExecute": false, "SkipReason": result.SkipReason})
		// #endregion
		return result
	}

	// 6. 价差检查（使用固定阈值）
	shouldTrigger := ctx.diffValue > ctx.threshold

	if !shouldTrigger {
		result.SkipReason = fmt.Sprintf("价差未达到阈值: %.6f%% (阈值: %.6f%%)", ctx.diffValue, ctx.threshold)
		// #region agent log
		writeBMDebug("brick_moving_trigger.go:checkAndExecuteOrderV2", "return skip", "H1", map[string]interface{}{"ShouldExecute": false, "SkipReason": result.SkipReason, "diffValue": ctx.diffValue, "threshold": ctx.threshold})
		// #endregion
		return result
	}

	// 7. 余额检查：从 position 获取可用 size，余额不足或不满足条件时不触发
	orderSize, _ := t.calculateOrderSize(ctx.direction)
	dirStr := t.getDirectionString(ctx.direction)
	exchangeType := t.getExchangeType()
	chainIndex := t.GetChainId()
	onchainTrader := t.getOnchainTrader()
	onchainPriceData := t.getOnchainPriceData(ctx.priceData, onchainTrader)
	pm := position.GetPositionManager()
	if orderSize > 0 {
		if pm == nil {
			result.SkipReason = "无法进行余额检查（PositionManager 未初始化）"
			writeBMDebug("brick_moving_trigger.go:checkAndExecuteOrderV2", "return skip", "H1", map[string]interface{}{"ShouldExecute": false, "SkipReason": result.SkipReason})
			t.logger.Debugf("跳过下单 %s: %s", ctx.direction, result.SkipReason)
			return result
		}
		availableSize := pm.GetAvailableBalanceSize(t.symbol, dirStr, exchangeType, chainIndex, ctx.priceData, onchainPriceData, t.traderAType, t.traderBType)
		if availableSize <= 0 {
			result.SkipReason = "无法获取可用余额或余额为 0"
			// #region agent log
			writeBMDebug("brick_moving_trigger.go:checkAndExecuteOrderV2", "return skip", "H1", map[string]interface{}{"ShouldExecute": false, "SkipReason": result.SkipReason})
			// #endregion
			t.logger.Debugf("跳过下单 %s: %s", ctx.direction, result.SkipReason)
			return result
		}
		if orderSize > availableSize {
			result.SkipReason = fmt.Sprintf("余额不足: 可用=%.6f, 计划=%.6f", availableSize, orderSize)
			// #region agent log
			writeBMDebug("brick_moving_trigger.go:checkAndExecuteOrderV2", "return skip", "H1", map[string]interface{}{"ShouldExecute": false, "SkipReason": result.SkipReason})
			// #endregion
			t.logger.Debugf("跳过下单 %s: %s", ctx.direction, result.SkipReason)
			return result
		}
	}

	// 8. 仓位不均衡检查（可选，BrickMovingTrigger 可以跳过）
	// 对于搬砖 trigger，可以跳过仓位不均衡检查，因为搬砖的目的是平衡仓位

	// 9. 所有检查通过，执行订单
	result.ShouldExecute = true
	// #region agent log
	writeBMDebug("brick_moving_trigger.go:checkAndExecuteOrderV2", "ShouldExecute=true", "H1", map[string]interface{}{"ShouldExecute": true, "direction": fmt.Sprint(ctx.direction), "symbol": t.symbol})
	// #endregion
	t.logger.Infof("触发下单 %s: 价差=%.6f%%, 阈值=%.6f%%", ctx.direction, ctx.diffValue, ctx.threshold)

	// 执行订单
	t.executeOrderV2(ctx)

	return result
}

// validatePriceData 验证价格数据有效性
func (t *BrickMovingTrigger) validatePriceData(priceData *model.PriceData) bool {
	if priceData == nil {
		return false
	}
	return priceData.BidPrice > 0 && priceData.AskPrice > 0
}

// executeOrderV2 执行订单（复用原有 trigger 的执行逻辑）
func (t *BrickMovingTrigger) executeOrderV2(ctx *OrderCheckContext) {
	// #region agent log
	writeBMDebug("brick_moving_trigger.go:executeOrderV2", "entry", "H4", map[string]interface{}{"direction": fmt.Sprint(ctx.direction), "symbol": t.symbol})
	// #endregion
	t.logger.Infof(">>> 发起订单 %s | 价差: %.6f%%", ctx.direction, ctx.diffValue)

	// 1. 准备执行上下文
	execCtx, shouldReturn := t.prepareOrderContext(ctx)
	// #region agent log
	writeBMDebug("brick_moving_trigger.go:executeOrderV2", "after prepareOrderContext", "H2", map[string]interface{}{"shouldReturn": shouldReturn, "hasExecCtx": execCtx != nil})
	// #endregion
	if shouldReturn {
		return
	}

	// 1.5 按方向标记腿为 pending，供 Web 展示
	if ctx.direction == DirectionAB {
		t.setTradeStatusAB("Apending", "Bpending")
	} else {
		t.setTradeStatusBA("Bpending", "Apending")
	}

	// 2. 初始化监控并设置 panic 恢复
	execCtx.monitorInstance = monitor.GetExecutionMonitor()
	var isAOnchain, isBOnchain bool
	if execCtx.traderA != nil {
		if _, ok := execCtx.traderA.(trader.OnchainTrader); ok {
			isAOnchain = true
		}
	}
	if execCtx.traderB != nil {
		if _, ok := execCtx.traderB.(trader.OnchainTrader); ok {
			isBOnchain = true
		}
	}
	execCtx.record = execCtx.monitorInstance.StartExecution(t.symbol, execCtx.directionStr, ctx.diffValue, ctx.threshold, execCtx.orderSize, isAOnchain, isBOnchain)
	defer t.handleOrderPanic(execCtx)

	// 3. 根据执行模式执行交易
	var orderA, orderB *model.Order
	var onchainResultA, onchainResultB *trader.OnchainTradeResult
	var err error

	if execCtx.needsSequential {
		orderA, orderB, onchainResultA, onchainResultB, err = t.executeTradersSequentially(ctx, execCtx)
	} else {
		orderA, orderB, onchainResultA, onchainResultB, err = t.executeTradersConcurrently(ctx, execCtx)
	}

	if err != nil {
		// 执行失败时保留 pending 或由子函数已设为失败，此处不再改；延迟重置该方向状态
		t.scheduleResetTradeStatus(ctx.direction)
		// #region agent log
		writeBMDebug("brick_moving_trigger.go:executeOrderV2", "err return without record", "H1", map[string]interface{}{"symbol": t.symbol, "err": err.Error()})
		// #endregion
		return // 错误已在执行函数中处理
	}

	// 3.5 全部成功，统一标记该方向完成；延迟重置
	if ctx.direction == DirectionAB {
		t.setTradeStatusAB("A完成", "B完成")
	} else {
		t.setTradeStatusBA("B完成", "A完成")
	}
	t.scheduleResetTradeStatus(ctx.direction)

	// 4. 计算实际大小并记录统计
	actualSize, _, _ := t.calculateActualSizes(orderA, orderB, execCtx.orderSize)
	t.logOrderCompletion(ctx, execCtx, orderA, orderB, actualSize, execCtx.orderSize)

	// 计划数量：顺序执行且第二腿已按链上结果调整时用调整后的数量，否则用原始 orderSize
	recordedPlanSize := execCtx.orderSize
	if execCtx.needsSequential {
		if execCtx.sequentialFirstA {
			recordedPlanSize = execCtx.orderB.Quantity
		} else {
			recordedPlanSize = execCtx.orderA.Quantity
		}
	}
	if recordedPlanSize <= 0 {
		recordedPlanSize = execCtx.orderSize
	}

	// 5. 单独协程：链上/交易所各轮询最多 30s 取完整数据后再落库（仅此一种记录方式）
	// #region agent log
	writeBMDebug("brick_moving_trigger.go:executeOrderV2", "about to recordTradeToStatisticsAsync", "H5", map[string]interface{}{"symbol": t.symbol, "directionStr": execCtx.directionStr, "recordedPlanSize": recordedPlanSize})
	// #endregion
	t.recordTradeToStatisticsAsync(ctx, execCtx, recordedPlanSize, orderA, orderB, onchainResultA, onchainResultB, isAOnchain, isBOnchain)

	// 6. 交易成功后，刷新余额
	if isAOnchain || isBOnchain {
		positionManager := position.GetPositionManager()
		if positionManager != nil {
			go func() {
				t.logger.Debugf("交易成功，触发余额刷新")
				positionManager.ForceRefreshAndUpdate(t.symbol)
			}()
		}
	}
}

// prepareOrderContext 准备订单执行上下文（简化版，不使用 analytics）
func (t *BrickMovingTrigger) prepareOrderContext(ctx *OrderCheckContext) (*orderExecutionContext, bool) {
	execCtx := &orderExecutionContext{
		directionStr:      t.getDirectionString(ctx.direction),
		exchangePriceData: ctx.priceData,
	}

	// 获取链上价格数据（如果有链上 trader）
	onchainTrader := t.getOnchainTrader()
	execCtx.onchainPriceData = t.getOnchainPriceData(execCtx.exchangePriceData, onchainTrader)

	// 获取交易所类型
	execCtx.exchangeType = t.getExchangeType()

	// 计算订单大小（按方向使用 triggerABSize / triggerBASize）
	orderSize, ok := t.calculateOrderSize(ctx.direction)
	// #region agent log
	writeBMDebug("brick_moving_trigger.go:prepareOrderContext", "after calculateOrderSize", "H2", map[string]interface{}{"orderSize": orderSize, "ok": ok})
	// #endregion
	if !ok {
		// #region agent log
		writeBMDebug("brick_moving_trigger.go:prepareOrderContext", "return nil,true", "H2", map[string]interface{}{"reason": "!ok"})
		// #endregion
		return nil, true
	}
	execCtx.orderSize = orderSize

	// 确定 trader A 和 trader B（根据方向）
	execCtx.traderA, execCtx.traderB = t.determineTradersForDirection(ctx.direction)

	// 构建订单请求
	execCtx.orderA, execCtx.orderB = t.buildOrderRequests(ctx.direction, orderSize)

	// 判断是否需要顺序执行及顺序：有链上则先链上后交易所；双交易所则并发
	execCtx.needsSequential, execCtx.sequentialFirstA = t.shouldExecuteSequentiallyWithOrder(execCtx.traderA, execCtx.traderB)

	return execCtx, false
}

// getDirectionString 获取方向字符串
func (t *BrickMovingTrigger) getDirectionString(direction OrderDirection) string {
	if direction == DirectionAB {
		return "AB"
	}
	return "BA"
}

// getOnchainPriceData 获取链上价格数据
func (t *BrickMovingTrigger) getOnchainPriceData(exchangePriceData *model.PriceData, chainTrader trader.OnchainTrader) *model.PriceData {
	if chainTrader != nil {
		latestSwapTx := chainTrader.GetLatestSwapTx()
		if latestSwapTx != nil {
			// 暂时使用交易所价格作为替代
			return exchangePriceData
		}
	}
	return exchangePriceData
}

// getExchangeType 获取交易所类型
func (t *BrickMovingTrigger) getExchangeType() string {
	// 优先从 sourceB 获取（通常是交易所）
	if t.sourceB != nil {
		if _, ok := t.sourceB.(trader.OnchainTrader); !ok {
			return extractExchangeType(t.sourceB.GetType())
		}
	}
	// 如果 sourceB 是链上，尝试从 sourceA 获取
	if t.sourceA != nil {
		if _, ok := t.sourceA.(trader.OnchainTrader); !ok {
			return extractExchangeType(t.sourceA.GetType())
		}
	}
	return ""
}

// calculateOrderSize 按方向使用 Web 配置的 triggerABSize/triggerBASize
func (t *BrickMovingTrigger) calculateOrderSize(direction OrderDirection) (float64, bool) {
	configured := t.getConfiguredSizeForDirection(direction)
	orderSize := configured
	if orderSize <= 0 {
		orderSize = 1000.0 // 未配置时默认 1000
		t.logger.Debugf("Configured size <= 0 for direction %v, using default 1000", direction)
	}
	// #region agent log
	writeBMDebug("brick_moving_trigger.go:calculateOrderSize", "return", "H3", map[string]interface{}{"configuredSize": configured, "orderSize": orderSize, "ok": true})
	// #endregion
	return orderSize, true
}

// determineTradersForDirection 根据方向确定 trader A 和 trader B
func (t *BrickMovingTrigger) determineTradersForDirection(direction OrderDirection) (trader.Trader, trader.Trader) {
	// A 始终是 sourceA，B 始终是 sourceB
	return t.sourceA, t.sourceB
}

// shouldExecuteSequentiallyWithOrder 判断是否需要顺序执行及顺序。
// 规则：1.A 链 B 交易所→先 A 后 B  2.B 链 A 交易所→先 B 后 A  3.双交易所→并发  4.双链→先 A 后 B
func (t *BrickMovingTrigger) shouldExecuteSequentiallyWithOrder(traderA, traderB trader.Trader) (needsSequential bool, firstA bool) {
	aOnchain := false
	if traderA != nil {
		_, aOnchain = traderA.(trader.OnchainTrader)
	}
	bOnchain := false
	if traderB != nil {
		_, bOnchain = traderB.(trader.OnchainTrader)
	}
	if !aOnchain && !bOnchain {
		return false, true // 双交易所，并发
	}
	// 有链上则顺序执行；先执行链上的一侧
	needsSequential = true
	firstA = aOnchain // A 为链则先 A；B 为链则先 B；双链则先 A
	return needsSequential, firstA
}

// marketTypeFromTraderType 根据 traderType（如 "okx:spot" / "binance:futures"）返回应使用的 model.MarketType，保证 size = 币数量（base）
func marketTypeFromTraderType(traderType string) model.MarketType {
	_, _, marketType, err := parseTraderTypeFromString(traderType)
	if err != nil {
		return model.MarketTypeFutures // 解析失败时与项目默认一致
	}
	if marketType == "spot" {
		return model.MarketTypeSpot
	}
	return model.MarketTypeFutures
}

// buildOrderRequests 根据方向构建 A 和 B 的订单请求；MarketType 按 traderAType/traderBType 区分现货/合约，保证 Quantity 为币数量（base）
func (t *BrickMovingTrigger) buildOrderRequests(direction OrderDirection, orderSize float64) (*model.PlaceOrderRequest, *model.PlaceOrderRequest) {
	marketTypeA := marketTypeFromTraderType(t.traderAType)
	marketTypeB := marketTypeFromTraderType(t.traderBType)
	if direction == DirectionAB {
		// +A-B: A 买入，B 卖出
		return &model.PlaceOrderRequest{
				Symbol:     t.symbol,
				Side:       model.OrderSideBuy,
				Type:       model.OrderTypeMarket,
				Quantity:   orderSize,
				MarketType: marketTypeA,
			}, &model.PlaceOrderRequest{
				Symbol:     t.symbol,
				Side:       model.OrderSideSell,
				Type:       model.OrderTypeMarket,
				Quantity:   orderSize,
				MarketType: marketTypeB,
			}
	}
	// -A+B: A 卖出，B 买入
	return &model.PlaceOrderRequest{
			Symbol:     t.symbol,
			Side:       model.OrderSideSell,
			Type:       model.OrderTypeMarket,
			Quantity:   orderSize,
			MarketType: marketTypeA,
		}, &model.PlaceOrderRequest{
			Symbol:     t.symbol,
			Side:       model.OrderSideBuy,
			Type:       model.OrderTypeMarket,
			Quantity:   orderSize,
			MarketType: marketTypeB,
		}
}

// reverseOrder 构造反向订单请求（Buy→Sell, Sell→Buy），用于单边失败时回滚成功端
func reverseOrder(orig *model.PlaceOrderRequest) *model.PlaceOrderRequest {
	reversedSide := model.OrderSideSell
	if orig.Side == model.OrderSideSell {
		reversedSide = model.OrderSideBuy
	}
	return &model.PlaceOrderRequest{
		Symbol:     orig.Symbol,
		Side:       reversedSide,
		Type:       model.OrderTypeMarket,
		Quantity:   orig.Quantity,
		MarketType: orig.MarketType,
	}
}

// rollbackTraderOrder 回滚已成功一端的订单（反向交易），记录日志；如果回滚也失败则打 CRITICAL
func (t *BrickMovingTrigger) rollbackTraderOrder(tr trader.Trader, origOrder *model.PlaceOrderRequest, direction OrderDirection, isA bool, record *monitor.ExecutionRecord) {
	side := "A"
	if !isA {
		side = "B"
	}
	reverseReq := reverseOrder(origOrder)
	_, _, rollbackErr := t.executeTraderOrder(tr, reverseReq, direction, isA, record)
	if rollbackErr != nil {
		t.logger.Errorf("[CRITICAL] 回滚 %s 端反向订单失败: %v — 存在不对称仓位，请手动处理", side, rollbackErr)
	} else {
		t.logger.Warnf("已自动回滚 %s 端订单（反向 %s %.4f %s）", side, reverseReq.Side, reverseReq.Quantity, reverseReq.Symbol)
	}
}

// handleOrderPanic 处理订单执行过程中的 panic
func (t *BrickMovingTrigger) handleOrderPanic(execCtx *orderExecutionContext) {
	if r := recover(); r != nil {
		errMsg := fmt.Sprintf("Panic: %v", r)
		execCtx.monitorInstance.FailExecution(execCtx.record, errMsg)
		t.logger.Errorf("执行订单时发生 Panic: %v", r)
	}
}

// executeTradersSequentially 顺序执行交易（先链上后交易所：可能先 A 后 B 或先 B 后 A）
func (t *BrickMovingTrigger) executeTradersSequentially(ctx *OrderCheckContext, execCtx *orderExecutionContext) (*model.Order, *model.Order, *trader.OnchainTradeResult, *trader.OnchainTradeResult, error) {
	firstA := execCtx.sequentialFirstA
	if firstA {
		t.logger.Infof(">>> 订单执行 %s | 模式: 顺序执行（先A后B）", ctx.direction)
		return t.executeTradersSequentialAB(ctx, execCtx)
	}
	t.logger.Infof(">>> 订单执行 %s | 模式: 顺序执行（先B后A）", ctx.direction)
	return t.executeTradersSequentialBA(ctx, execCtx)
}

// executeTradersSequentialAB 先 A 后 B 的顺序执行（A→B 方向）
func (t *BrickMovingTrigger) executeTradersSequentialAB(ctx *OrderCheckContext, execCtx *orderExecutionContext) (*model.Order, *model.Order, *trader.OnchainTradeResult, *trader.OnchainTradeResult, error) {
	// 1. 先执行 A
	orderA, onchainResultA, err := t.executeTraderOrder(execCtx.traderA, execCtx.orderA, ctx.direction, true, execCtx.record)
	if err != nil {
		t.setTradeStatusAB("A失败", "Bpending")
		t.logger.Errorf(">>> 订单失败(A) %s | 错误: %v", ctx.direction, err)
		return nil, nil, nil, nil, err
	}
	t.setTradeStatusAB("A完成", "Bpending")

	// 2. 若 A 是链上且有返回数据，用链上实际数量推算 B 的 size，保证两边一致（滑点后）；链上结果为空时沿用原计划 size（不修改 orderB.Quantity）
	if _, isChain := execCtx.traderA.(trader.OnchainTrader); isChain && onchainResultA != nil {
		if size, ok := t.sizeForExchangeAfterChain(onchainResultA, ctx.direction, true, execCtx.exchangePriceData); ok && size > 0 {
			execCtx.orderB.Quantity = roundQuantityForExchange(size)
		} else {
			t.logger.Infof("  [订单调整] 链上 A 结果为空或无法推算 size，B 端使用原计划 size=%.6f", execCtx.orderB.Quantity)
		}
	}

	// 3. 执行 B（有链上结果时已按链上实际数量调整 Quantity，否则用配置 orderSize）
	orderB, onchainResultB, err := t.executeTraderOrder(execCtx.traderB, execCtx.orderB, ctx.direction, false, execCtx.record)
	if err != nil {
		t.setTradeStatusAB("A完成", "B失败")
		t.logger.Errorf(">>> 订单失败(B) %s | 错误: %v — 回滚 A 端", ctx.direction, err)
		t.rollbackTraderOrder(execCtx.traderA, execCtx.orderA, ctx.direction, true, execCtx.record)
		return nil, nil, nil, nil, fmt.Errorf("B side failed: %w; A side rolled back", err)
	}
	t.setTradeStatusAB("A完成", "B完成")
	return orderA, orderB, onchainResultA, onchainResultB, nil
}

// executeTradersSequentialBA 先 B 后 A 的顺序执行（B→A 方向）
func (t *BrickMovingTrigger) executeTradersSequentialBA(ctx *OrderCheckContext, execCtx *orderExecutionContext) (*model.Order, *model.Order, *trader.OnchainTradeResult, *trader.OnchainTradeResult, error) {
	// 1. 先执行 B
	orderB, onchainResultB, err := t.executeTraderOrder(execCtx.traderB, execCtx.orderB, ctx.direction, false, execCtx.record)
	if err != nil {
		t.setTradeStatusBA("B失败", "Apending")
		t.logger.Errorf(">>> 订单失败(B) %s | 错误: %v", ctx.direction, err)
		return nil, nil, nil, nil, err
	}
	t.setTradeStatusBA("B完成", "Apending")

	// 2. 若 B 是链上且有返回数据，用链上实际数量推算 A 的 size，保证两边一致（滑点后）；链上结果为空时沿用原计划 size（不修改 orderA.Quantity）
	if _, isChain := execCtx.traderB.(trader.OnchainTrader); isChain && onchainResultB != nil {
		if size, ok := t.sizeForExchangeAfterChain(onchainResultB, ctx.direction, false, execCtx.exchangePriceData); ok && size > 0 {
			execCtx.orderA.Quantity = roundQuantityForExchange(size)
		} else {
			t.logger.Infof("  [订单调整] 链上 B 结果为空或无法推算 size，A 端使用原计划 size=%.6f", execCtx.orderA.Quantity)
		}
	}

	// 3. 执行 A（有链上结果时已按链上实际数量调整 Quantity，否则用配置 orderSize）
	orderA, onchainResultA, err := t.executeTraderOrder(execCtx.traderA, execCtx.orderA, ctx.direction, true, execCtx.record)
	if err != nil {
		t.setTradeStatusBA("B完成", "A失败")
		t.logger.Errorf(">>> 订单失败(A) %s | 错误: %v — 回滚 B 端", ctx.direction, err)
		t.rollbackTraderOrder(execCtx.traderB, execCtx.orderB, ctx.direction, false, execCtx.record)
		return nil, nil, nil, nil, fmt.Errorf("A side failed: %w; B side rolled back", err)
	}
	t.setTradeStatusBA("B完成", "A完成")
	return orderA, orderB, onchainResultA, onchainResultB, nil
}

// roundQuantityForExchange 将数量圆整到交易所允许的精度（如 Bitget checkScale=2 要求最多 2 位小数），避免链上 4.977 等导致拒单
func roundQuantityForExchange(size float64) float64 {
	const decimals = 2
	pow := math.Pow(10, float64(decimals))
	return math.Round(size*pow) / pow
}

// sizeForExchangeAfterChain 根据链上确认后的实际成交数量计算交易所侧 size，保证两腿币数量一致。
// 规则：
// 1. 链上买入币（有滑点）→ 交易所卖出：size = 链上实际得到的币数量（滑点后的 CoinAmount/AmountOut），保证币本位一致。
// 2. 链上卖出币（无滑点影响 size）→ 交易所买入：size = 链上卖出的币数量（AmountIn），直接用原计划 size，不需要用计价币/价格推算。
func (t *BrickMovingTrigger) sizeForExchangeAfterChain(onchainResult *trader.OnchainTradeResult, direction OrderDirection, firstIsA bool, exchangePriceData *model.PriceData) (size float64, ok bool) {
	if onchainResult == nil {
		return 0, false
	}
	// 链上卖出(得 USDT)：交易所侧为买入，size = 链上卖出的币数量（AmountIn），币本位保持一致
	chainGotUSDT := (direction == DirectionAB && !firstIsA) || (direction == DirectionBA && firstIsA)
	// 链上买入(得币)：交易所侧为卖出，size = 链上实际得到的币数量（有滑点），币本位保持一致
	chainGotCoin := (direction == DirectionAB && firstIsA) || (direction == DirectionBA && !firstIsA)

	if chainGotUSDT {
		// 链上卖出时，我们输入的是币， AmountIn = 卖出的币数量，直接用此值作为交易所买入 size
		coinSold := onchainResult.AmountInFloat
		if coinSold <= 0 && onchainResult.CoinAmount > 0 {
			coinSold = onchainResult.CoinAmount // 兜底
		}
		if coinSold <= 0 {
			return 0, false
		}
		size = coinSold
		t.logger.Infof("  [订单调整] 链上卖出币=%.6f，交易所买入 size=%.6f（币本位一致）", coinSold, size)
		return size, true
	}
	if chainGotCoin {
		coinAmount := onchainResult.CoinAmount
		if coinAmount <= 0 {
			// 兜底：解析为 0 时用 AmountOut（链上买入得币=AmountOut）或 AmountIn（链上卖出耗币=AmountIn）
			if onchainResult.AmountOutFloat > 0 {
				coinAmount = onchainResult.AmountOutFloat
				t.logger.Warnf("  [订单调整] CoinAmount 为空，用 AmountOutFloat 兜底=%.6f", coinAmount)
			} else if onchainResult.AmountInFloat > 0 {
				coinAmount = onchainResult.AmountInFloat
				t.logger.Warnf("  [订单调整] CoinAmount 为空，用 AmountInFloat 兜底=%.6f", coinAmount)
			}
		}
		if coinAmount <= 0 {
			return 0, false
		}
		size = coinAmount
		t.logger.Infof("  [订单调整] 链上得到币=%.6f，交易所侧 size=%.6f", coinAmount, size)
		return size, true
	}
	return 0, false
}

// executeTradersConcurrently 并发执行交易（A 和 B 同时执行）
func (t *BrickMovingTrigger) executeTradersConcurrently(ctx *OrderCheckContext, execCtx *orderExecutionContext) (*model.Order, *model.Order, *trader.OnchainTradeResult, *trader.OnchainTradeResult, error) {
	t.logger.Infof(">>> 订单执行 %s | 模式: 并发执行（A和B同时）", ctx.direction)

	type result struct {
		order   *model.Order
		onchain *trader.OnchainTradeResult
		err     error
		isA     bool
	}

	resultChan := make(chan result, 2)

	// 并发执行 A
	go func() {
		order, onchainResult, err := t.executeTraderOrder(execCtx.traderA, execCtx.orderA, ctx.direction, true, execCtx.record)
		resultChan <- result{order: order, onchain: onchainResult, err: err, isA: true}
	}()

	// 并发执行 B
	go func() {
		order, onchainResult, err := t.executeTraderOrder(execCtx.traderB, execCtx.orderB, ctx.direction, false, execCtx.record)
		resultChan <- result{order: order, onchain: onchainResult, err: err, isA: false}
	}()

	// 等待两个结果
	var orderA, orderB *model.Order
	var onchainResultA, onchainResultB *trader.OnchainTradeResult
	var errA, errB error

	for i := 0; i < 2; i++ {
		res := <-resultChan
		if res.isA {
			orderA, onchainResultA, errA = res.order, res.onchain, res.err
		} else {
			orderB, onchainResultB, errB = res.order, res.onchain, res.err
		}
	}

	// 处理错误：单边成功 + 单边失败时，回滚成功端
	if errA != nil && errB != nil {
		if ctx.direction == DirectionAB {
			t.setTradeStatusAB("A失败", "B失败")
		} else {
			t.setTradeStatusBA("B失败", "A失败")
		}
		t.logger.Errorf(">>> 订单失败(A+B) %s | A错误: %v, B错误: %v", ctx.direction, errA, errB)
		return nil, nil, nil, nil, fmt.Errorf("both sides failed: A=%v, B=%v", errA, errB)
	}
	if errA != nil {
		if ctx.direction == DirectionAB {
			t.setTradeStatusAB("A失败", "B完成")
		} else {
			t.setTradeStatusBA("B完成", "A失败")
		}
		t.logger.Errorf(">>> 订单失败(A) %s | 错误: %v — B 已成功，回滚 B 端", ctx.direction, errA)
		t.rollbackTraderOrder(execCtx.traderB, execCtx.orderB, ctx.direction, false, execCtx.record)
		return nil, nil, nil, nil, fmt.Errorf("A side failed: %w; B side rolled back", errA)
	}
	if errB != nil {
		if ctx.direction == DirectionAB {
			t.setTradeStatusAB("A完成", "B失败")
		} else {
			t.setTradeStatusBA("B失败", "A完成")
		}
		t.logger.Errorf(">>> 订单失败(B) %s | 错误: %v — A 已成功，回滚 A 端", ctx.direction, errB)
		t.rollbackTraderOrder(execCtx.traderA, execCtx.orderA, ctx.direction, true, execCtx.record)
		return nil, nil, nil, nil, fmt.Errorf("B side failed: %w; A side rolled back", errB)
	}

	return orderA, orderB, onchainResultA, onchainResultB, nil
}

// executeTraderOrder 统一执行 trader 订单（支持链上和交易所）
func (t *BrickMovingTrigger) executeTraderOrder(tr trader.Trader, req *model.PlaceOrderRequest, direction OrderDirection, isA bool, record *monitor.ExecutionRecord) (*model.Order, *trader.OnchainTradeResult, error) {
	if tr == nil {
		return nil, nil, fmt.Errorf("trader is nil")
	}

	monitorInstance := monitor.GetExecutionMonitor()

	// 检查是否是链上 trader
	if onchainTrader, ok := tr.(trader.OnchainTrader); ok {
		// 链上交易：使用 ExecuteOnChain
		chainIndex := t.onChainData.ChainIndex
		if chainIndex == "" {
			chainIndex = "56" // 默认 BSC
		}

		// 确定 swap 方向：+A-B 时 A=买入(Buy)、B=卖出(Sell)；-A+B 时 A=卖出(Sell)、B=买入(Buy)
		var swapDirection onchain.SwapDirection
		if direction == DirectionAB {
			if isA {
				// +A-B: A 买入（USDT -> Coin）
				swapDirection = onchain.SwapDirectionBuy
			} else {
				// +A-B: B 卖出（Coin -> USDT）
				swapDirection = onchain.SwapDirectionSell
			}
		} else {
			if isA {
				// -A+B: A 卖出（Coin -> USDT）
				swapDirection = onchain.SwapDirectionSell
			} else {
				// -A+B: B 买入（USDT -> Coin）
				swapDirection = onchain.SwapDirectionBuy
			}
		}

		txHash, onchainResult, err := onchainTrader.ExecuteOnChain(chainIndex, swapDirection)
		actualCoinAmount := float64(0)
		if onchainResult != nil {
			actualCoinAmount = onchainResult.CoinAmount
		}

		// 更新监控
		if isA {
			monitorInstance.UpdateA(record, txHash, "", actualCoinAmount, 0, err)
		} else {
			monitorInstance.UpdateB(record, txHash, "", actualCoinAmount, 0, err)
		}

		if err != nil {
			return nil, nil, err
		}

		// 构建 Order 对象（链上交易的结果）
		order := &model.Order{
			OrderID:     txHash,
			FilledQty:   actualCoinAmount,
			FilledPrice: 0, // 链上交易的价格需要从其他地方获取
			Quantity:    req.Quantity,
		}

		return order, onchainResult, nil
	}

	// 交易所交易：使用 ExecuteOrder
	order, err := tr.ExecuteOrder(req)
	actualQuantity := float64(0)
	actualPrice := float64(0)

	if order != nil {
		if order.FilledQty > 0 {
			actualQuantity = order.FilledQty
			actualPrice = order.FilledPrice
		} else {
			actualQuantity = req.Quantity
		}

		orderType := "B"
		if isA {
			orderType = "A"
		}
		t.logger.Infof("🔍 [%s订单详情] OrderID=%s, FilledQty=%.6f, FilledPrice=%.6f, Fee=%.6f",
			orderType, order.OrderID, order.FilledQty, order.FilledPrice, order.Fee)
	}

	// 更新监控
	orderID := ""
	if order != nil {
		orderID = order.OrderID
	}
	if isA {
		monitorInstance.UpdateA(record, "", orderID, actualQuantity, actualPrice, err)
	} else {
		monitorInstance.UpdateB(record, "", orderID, actualQuantity, actualPrice, err)
	}

	if err != nil {
		return nil, nil, err
	}

	return order, nil, nil
}

// calculateActualSizes 计算实际成交大小
func (t *BrickMovingTrigger) calculateActualSizes(orderA, orderB *model.Order, plannedSize float64) (float64, float64, float64) {
	var actualCoinAmount float64       // A 的成交数量（如果是链上）
	var actualExchangeQuantity float64 // B 的成交数量（如果是交易所）

	if orderA != nil && orderA.FilledQty > 0 {
		actualCoinAmount = orderA.FilledQty
	}
	if orderB != nil && orderB.FilledQty > 0 {
		actualExchangeQuantity = orderB.FilledQty
	}

	// 实际大小优先使用 A 的成交数量，否则使用 B 的成交数量
	actualSize := actualCoinAmount
	if actualSize <= 0 {
		actualSize = actualExchangeQuantity
	}
	if actualSize <= 0 {
		actualSize = plannedSize // 兜底使用计划数量
	}

	return actualSize, actualCoinAmount, actualExchangeQuantity
}

// logOrderCompletion 记录订单完成日志
func (t *BrickMovingTrigger) logOrderCompletion(ctx *OrderCheckContext, execCtx *orderExecutionContext, orderA, orderB *model.Order, actualSize, orderSize float64) {
	actualCoinAmount := float64(0)
	actualExchangeQuantity := float64(0)

	if orderA != nil {
		actualCoinAmount = orderA.FilledQty
	}
	if orderB != nil {
		actualExchangeQuantity = orderB.FilledQty
	}

	// 判断是否有链上交易
	_, hasOnchain := execCtx.traderA.(trader.OnchainTrader)

	if hasOnchain && actualCoinAmount > 0 {
		t.logger.Infof(">>> 订单完成 %s | A成交: %.6f, B成交: %.6f, 实际size: %.6f (原计划: %.6f)",
			ctx.direction, actualCoinAmount, actualExchangeQuantity, actualSize, orderSize)
	} else {
		t.logger.Infof(">>> 订单完成 %s | A成交: %.6f, B成交: %.6f, 实际size: %.6f (原计划: %.6f)",
			ctx.direction, actualCoinAmount, actualExchangeQuantity, actualSize, orderSize)
	}
}

// recordTradeToStatisticsAsync 成交完成后单独协程：链上轮询直到拿到完整兑换记录，交易所轮询直到拿到完整成交，最多等 30s，再落库；仅保留此一种记录方式
func (t *BrickMovingTrigger) recordTradeToStatisticsAsync(ctx *OrderCheckContext, execCtx *orderExecutionContext,
	orderSize float64, orderA, orderB *model.Order, onchainResultA, onchainResultB *trader.OnchainTradeResult,
	isAOnchain, isBOnchain bool) {
	const dataGatherTimeout = 30 * time.Second

	parallel.GoSafe(func() {
		orderAUpdated := orderA
		orderBUpdated := orderB
		onchainA := onchainResultA
		onchainB := onchainResultB

		chainIndex := t.onChainData.ChainIndex
		if chainIndex == "" {
			chainIndex = "56"
		}

		// 链上：轮询直到拿到完整 AmountIn/AmountOut 或超时
		if isAOnchain && orderA != nil && orderA.OrderID != "" {
			if ot, ok := execCtx.traderA.(trader.OnchainTrader); ok {
				dirA := onchain.SwapDirectionBuy
				if execCtx.directionStr == "BA" {
					dirA = onchain.SwapDirectionSell
				}
				if res, err := ot.WaitForFullTxResult(orderA.OrderID, chainIndex, dirA, dataGatherTimeout); err == nil && res != nil {
					onchainA = res
				}
			}
		}
		if isBOnchain && orderB != nil && orderB.OrderID != "" {
			if ot, ok := execCtx.traderB.(trader.OnchainTrader); ok {
				dirB := onchain.SwapDirectionSell
				if execCtx.directionStr == "BA" {
					dirB = onchain.SwapDirectionBuy
				}
				if res, err := ot.WaitForFullTxResult(orderB.OrderID, chainIndex, dirB, dataGatherTimeout); err == nil && res != nil {
					onchainB = res
				}
			}
		}

		// 交易所：轮询直到拿到完整 FilledQty/FilledPrice 或超时
		if !isAOnchain && orderA != nil && orderA.OrderID != "" && execCtx.orderA != nil {
			if o := t.pollCexOrderUntilFilledWithTimeout(execCtx.traderA, execCtx.orderA.Symbol, orderA.OrderID, execCtx.orderA.MarketType, dataGatherTimeout); o != nil {
				orderAUpdated = o
			}
		}
		if !isBOnchain && orderB != nil && orderB.OrderID != "" && execCtx.orderB != nil {
			if o := t.pollCexOrderUntilFilledWithTimeout(execCtx.traderB, execCtx.orderB.Symbol, orderB.OrderID, execCtx.orderB.MarketType, dataGatherTimeout); o != nil {
				orderBUpdated = o
			}
		}

		t.recordTradeToStatistics(ctx, execCtx.directionStr, orderSize, orderAUpdated, orderBUpdated, onchainA, onchainB, isAOnchain, isBOnchain, execCtx.exchangePriceData)
	})
}

// pollCexOrderUntilFilledWithTimeout 轮询 CEX 订单直到成交或超时（最多 timeout），返回完整成交信息的 Order
func (t *BrickMovingTrigger) pollCexOrderUntilFilledWithTimeout(tr trader.Trader, symbol, orderID string, marketType model.MarketType, timeout time.Duration) *model.Order {
	cex, ok := tr.(*trader.CexTrader)
	if !ok {
		return nil
	}
	deadline := time.Now().Add(timeout)
	interval := 500 * time.Millisecond
	for i := 0; time.Now().Before(deadline); i++ {
		if i > 0 {
			time.Sleep(interval)
		}
		o, err := cex.QueryOrderDetails(symbol, orderID, marketType)
		if err != nil {
			t.logger.Warnf("  [异步记录] 查询 CEX 成交失败(第%d次): %v", i+1, err)
			continue
		}
		if o != nil && o.FilledQty > 0 && o.FilledPrice > 0 {
			t.logger.Infof("  [异步记录] 获取完整成交 | OrderID=%s FilledQty=%.6f FilledPrice=%.6f Fee=%.6f", orderID, o.FilledQty, o.FilledPrice, o.Fee)
			return o
		}
	}
	t.logger.Warnf("  [异步记录] 轮询 %.0fs 后仍未获取完整成交，使用已有数据记录", timeout.Seconds())
	return nil
}

// recordTradeToStatistics 记录成交数据到 StatisticsManager（简化版，不依赖 analytics）
func (t *BrickMovingTrigger) recordTradeToStatistics(ctx *OrderCheckContext, directionStr string, orderSize float64,
	orderA, orderB *model.Order, onchainResultA, onchainResultB *trader.OnchainTradeResult,
	isAOnchain, isBOnchain bool, exchangePriceData *model.PriceData) {
	// #region agent log
	writeBMDebug("brick_moving_trigger.go:recordTradeToStatistics", "entry", "H1", map[string]interface{}{"symbol": t.symbol, "directionStr": directionStr})
	// #endregion
	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager == nil {
		// #region agent log
		writeBMDebug("brick_moving_trigger.go:recordTradeToStatistics", "statisticsManager nil", "H2", map[string]interface{}{"symbol": t.symbol})
		// #endregion
		return
	}

	// 根据方向确定 A 和 B 的操作
	var sideA, sideB model.OrderSide
	if directionStr == "AB" {
		sideA = model.OrderSideBuy  // A 买入
		sideB = model.OrderSideSell // B 卖出
	} else {
		sideA = model.OrderSideSell // A 卖出
		sideB = model.OrderSideBuy  // B 买入
	}

	// 计算 A 的成本/收入：严格优先使用查询结果（链上 AmountIn/AmountOut/Gas，交易所 FilledQty/FilledPrice/Fee），无结果时才用价格估算
	var costA, revenueA, feeA, gasFeeA float64
	if isAOnchain {
		// A 是链上：严格使用链上查询的输入/输出/Gas
		if onchainResultA != nil {
			gasFeeA = onchainResultA.GasFee
			if sideA == model.OrderSideBuy {
				costA = onchainResultA.AmountInFloat
				if costA <= 0 && onchainResultA.CoinAmount > 0 && exchangePriceData != nil && exchangePriceData.AskPrice > 0 {
					costA = onchainResultA.CoinAmount * exchangePriceData.AskPrice
				}
			} else {
				revenueA = onchainResultA.AmountOutFloat
				if revenueA <= 0 && onchainResultA.CoinAmount > 0 && exchangePriceData != nil && exchangePriceData.BidPrice > 0 {
					revenueA = onchainResultA.CoinAmount * exchangePriceData.BidPrice
				}
			}
		} else if orderA != nil && orderA.FilledQty > 0 && exchangePriceData != nil {
			if sideA == model.OrderSideBuy && exchangePriceData.AskPrice > 0 {
				costA = orderA.FilledQty * exchangePriceData.AskPrice
			} else if exchangePriceData.BidPrice > 0 {
				revenueA = orderA.FilledQty * exchangePriceData.BidPrice
			}
		}
	} else {
		// A 是交易所：严格使用订单查询的成交量、成交价、手续费
		if orderA != nil && orderA.FilledQty > 0 && orderA.FilledPrice > 0 {
			if sideA == model.OrderSideBuy {
				costA = orderA.FilledQty * orderA.FilledPrice
			} else {
				revenueA = orderA.FilledQty * orderA.FilledPrice
			}
			feeA = orderA.Fee
		}
		if (costA <= 0 && revenueA <= 0) && orderSize > 0 && exchangePriceData != nil {
			if directionStr == "AB" && exchangePriceData.AskPrice > 0 {
				costA = orderSize * exchangePriceData.AskPrice
			} else if directionStr == "BA" && exchangePriceData.BidPrice > 0 {
				revenueA = orderSize * exchangePriceData.BidPrice
			}
		}
	}

	// 计算 B 的成本/收入：严格优先使用查询结果
	var costB, revenueB, feeB, gasFeeB float64
	if isBOnchain {
		// B 是链上：严格使用链上查询的输入/输出/Gas
		if onchainResultB != nil {
			gasFeeB = onchainResultB.GasFee
			if sideB == model.OrderSideBuy {
				costB = onchainResultB.AmountInFloat
				if costB <= 0 && onchainResultB.CoinAmount > 0 && exchangePriceData != nil && exchangePriceData.AskPrice > 0 {
					costB = onchainResultB.CoinAmount * exchangePriceData.AskPrice
				}
			} else {
				revenueB = onchainResultB.AmountOutFloat
				if revenueB <= 0 && onchainResultB.CoinAmount > 0 && exchangePriceData != nil && exchangePriceData.BidPrice > 0 {
					revenueB = onchainResultB.CoinAmount * exchangePriceData.BidPrice
				}
			}
		} else if orderB != nil && orderB.FilledQty > 0 && exchangePriceData != nil {
			if sideB == model.OrderSideBuy && exchangePriceData.AskPrice > 0 {
				costB = orderB.FilledQty * exchangePriceData.AskPrice
			} else if exchangePriceData.BidPrice > 0 {
				revenueB = orderB.FilledQty * exchangePriceData.BidPrice
			}
		}
	} else {
		// B 是交易所：严格使用订单查询的成交量、成交价、手续费
		if orderB != nil && orderB.FilledQty > 0 && orderB.FilledPrice > 0 {
			if sideB == model.OrderSideBuy {
				costB = orderB.FilledQty * orderB.FilledPrice
			} else {
				revenueB = orderB.FilledQty * orderB.FilledPrice
			}
			feeB = orderB.Fee
		}
		if (revenueB <= 0 && costB <= 0) && orderSize > 0 && exchangePriceData != nil {
			if directionStr == "AB" && exchangePriceData.BidPrice > 0 {
				revenueB = orderSize * exchangePriceData.BidPrice
			} else if directionStr == "BA" && exchangePriceData.AskPrice > 0 {
				costB = orderSize * exchangePriceData.AskPrice
			}
		}
	}

	// 计算收益：总收入 - 总成本 - 总手续费 - 总 Gas 费（均以查询结果为优先）
	totalRevenue := revenueA + revenueB
	totalCost := costA + costB
	totalFee := feeA + feeB
	totalGasFee := gasFeeA + gasFeeB
	profit := totalRevenue - totalCost - totalFee - totalGasFee

	// 确定显示价格和 sizeUSDT
	var price float64
	var sizeUSDT float64
	if directionStr == "AB" {
		// +A-B: A 买入，B 卖出，使用 B 的卖出价格
		if orderB != nil && orderB.FilledPrice > 0 {
			price = orderB.FilledPrice
			sizeUSDT = revenueB
		} else if exchangePriceData != nil && exchangePriceData.BidPrice > 0 {
			price = exchangePriceData.BidPrice
			sizeUSDT = orderSize * price
		}
	} else {
		// -A+B: A 卖出，B 买入，使用 B 的买入价格
		if orderB != nil && orderB.FilledPrice > 0 {
			price = orderB.FilledPrice
			sizeUSDT = costB
		} else if exchangePriceData != nil && exchangePriceData.AskPrice > 0 {
			price = exchangePriceData.AskPrice
			sizeUSDT = orderSize * price
		}
	}

	t.logger.Infof("💰 收益(%s) | A收入=%.2f, A成本=%.2f, A手续费=%.2f, A Gas=%.6f | B收入=%.2f, B成本=%.2f, B手续费=%.2f, B Gas=%.6f | 总收益=%.2f USDT",
		directionStr, revenueA, costA, feeA, gasFeeA, revenueB, costB, feeB, gasFeeB, profit)

	// 币本位成本/磨损：costInCoin 用于统计；磨损百分比 = 总成本/总收入*100（成本占收入比例，更直观）
	var costInCoin float64
	var costPercent float64
	actualCostUSDT := totalCost + totalFee + totalGasFee
	if price > 0 && orderSize > 0 {
		costInCoin = actualCostUSDT / price
	}
	if totalRevenue > 0 {
		costPercent = (actualCostUSDT / totalRevenue) * 100.0
	} else if price > 0 && orderSize > 0 {
		costPercent = (costInCoin / orderSize) * 100.0
	}

	// 记录成本数据到 StatisticsManager（使用实际查询结果汇总）
	costDataForStats := &statistics.CostData{
		CostInCoin:    costInCoin,
		CostPercent:   costPercent,
		TotalCostUSDT: totalCost + totalFee + totalGasFee,
	}
	statisticsManager.RecordCost(t.symbol, costDataForStats)

	// 记录Size数据到 StatisticsManager
	sizeDataForStats := &statistics.SizeData{
		Size:     orderSize,
		SizeUSDT: sizeUSDT,
	}
	statisticsManager.RecordSize(t.symbol, sizeDataForStats)

	// 获取 A/B 成交量和成交价
	var filledQtyA, filledPriceA, filledQtyB, filledPriceB float64
	if isAOnchain && onchainResultA != nil && onchainResultA.CoinAmount > 0 {
		filledQtyA = onchainResultA.CoinAmount
		if costA > 0 {
			filledPriceA = costA / filledQtyA
		} else if revenueA > 0 {
			filledPriceA = revenueA / filledQtyA
		}
	} else if orderA != nil && orderA.FilledQty > 0 {
		filledQtyA = orderA.FilledQty
		filledPriceA = orderA.FilledPrice
	}
	// 回退：A 无法获取成交时，用 costA/revenueA 反推或 orderSize
	if filledQtyA <= 0 && (costA > 0 || revenueA > 0) && price > 0 {
		filledQtyA = (costA + revenueA) / price
		filledPriceA = price
	}
	if filledQtyA <= 0 && orderSize > 0 {
		filledQtyA = orderSize
		filledPriceA = price
	}

	if isBOnchain && onchainResultB != nil && onchainResultB.CoinAmount > 0 {
		filledQtyB = onchainResultB.CoinAmount
		if costB > 0 {
			filledPriceB = costB / filledQtyB
		} else if revenueB > 0 {
			filledPriceB = revenueB / filledQtyB
		}
	} else if orderB != nil && orderB.FilledQty > 0 {
		filledQtyB = orderB.FilledQty
		filledPriceB = orderB.FilledPrice
	}
	// 回退：B 无法获取成交（如 QueryFuturesOrder 不支持）时，用 costB/revenueB 反推或 orderSize
	if filledQtyB <= 0 && (costB > 0 || revenueB > 0) && price > 0 {
		filledQtyB = (costB + revenueB) / price
		filledPriceB = price
	}
	if filledQtyB <= 0 && orderSize > 0 {
		filledQtyB = orderSize
		filledPriceB = price
	}

	// 构建成交记录（输入/输出/均价/Gas/手续费均以查询结果为准）
	tradeRecord := &statistics.TradeRecord{
		Direction:    directionStr,
		Size:         orderSize,
		SizeUSDT:     sizeUSDT,
		Price:        price,
		DiffValue:    ctx.diffValue,
		Profit:       profit,
		CostInCoin:   costInCoin,
		CostPercent:  costPercent,
		FilledQtyA:   filledQtyA,
		FilledQtyB:   filledQtyB,
		FilledPriceA: filledPriceA,
		FilledPriceB: filledPriceB,
		FeeA:         feeA,
		FeeB:         feeB,
		GasA:         gasFeeA,
		GasB:         gasFeeB,
		RevenueA:     revenueA,
		CostA:        costA,
		RevenueB:     revenueB,
		CostB:        costB,
	}

	// #region agent log
	writeBMDebug("brick_moving_trigger.go:recordTradeToStatistics", "calling RecordTrade", "H1", map[string]interface{}{"symbol": t.symbol})
	// #endregion
	statisticsManager.RecordTrade(t.symbol, tradeRecord)
}

// ==================== proto.Trigger 接口实现 ====================

// GetID 获取 Trigger ID
func (t *BrickMovingTrigger) GetID() uint64 {
	return t.ID
}

// GetSymbol 获取交易对符号
func (t *BrickMovingTrigger) GetSymbol() string {
	return t.symbol
}

// GetStatus 获取 trigger 的运行状态
func (t *BrickMovingTrigger) GetStatus() string {
	if t.IsRunning() {
		return "running"
	}
	return "stopped"
}

// GetDirectionEnabled 获取方向的订单执行启用状态（实现 proto.Trigger 接口）
// direction: 0 表示 DirectionAB, 1 表示 DirectionBA
func (t *BrickMovingTrigger) GetDirectionEnabled(direction int) bool {
	if direction == 0 {
		return t.GetDirectionEnabledOrder(DirectionAB)
	} else if direction == 1 {
		return t.GetDirectionEnabledOrder(DirectionBA)
	}
	return false
}

// SetDirectionEnabled 设置方向的订单执行启用状态（实现 proto.Trigger 接口）
// direction: 0 表示 DirectionAB, 1 表示 DirectionBA
func (t *BrickMovingTrigger) SetDirectionEnabled(direction int, enabled bool) {
	if direction == 0 {
		t.SetDirectionEnabledOrder(DirectionAB, enabled)
	} else if direction == 1 {
		t.SetDirectionEnabledOrder(DirectionBA, enabled)
	}
}

// GetPriceData 获取当前价格数据（directionAB/directionBA），供价差展示等只读使用
func (t *BrickMovingTrigger) GetPriceData() (directionAB, directionBA *DirectionConfig) {
	return t.directionAB, t.directionBA
}

// GetMinThreshold 获取最小阈值（实现 proto.Trigger 接口）
// 对于 BrickMovingTrigger，返回 thresholdAB（+A-B 方向）
func (t *BrickMovingTrigger) GetMinThreshold() float64 {
	return t.GetThresholdAB()
}

// SetMinThreshold 设置最小阈值（实现 proto.Trigger 接口）
// 对于 BrickMovingTrigger，设置 thresholdAB（+A-B 方向）
func (t *BrickMovingTrigger) SetMinThreshold(minThreshold float64) error {
	t.SetThresholdAB(minThreshold)
	return nil
}

// GetMaxThreshold 获取最大阈值（实现 proto.Trigger 接口）
// 对于 BrickMovingTrigger，返回 thresholdBA（-A+B 方向）
func (t *BrickMovingTrigger) GetMaxThreshold() float64 {
	return t.GetThresholdBA()
}

// SetMaxThreshold 设置最大阈值（实现 proto.Trigger 接口）
// 对于 BrickMovingTrigger，设置 thresholdBA（-A+B 方向）
func (t *BrickMovingTrigger) SetMaxThreshold(maxThreshold float64) error {
	t.SetThresholdBA(maxThreshold)
	return nil
}

// GetTargetThresholdInterval 获取目标价差阈值区间（向后兼容）
func (t *BrickMovingTrigger) GetTargetThresholdInterval() float64 {
	return t.GetMinThreshold()
}

// SetTargetThresholdInterval 设置目标价差阈值区间（向后兼容）
func (t *BrickMovingTrigger) SetTargetThresholdInterval(interval float64) error {
	return t.SetMinThreshold(interval)
}

// GetOptimalThresholds 获取最优阈值区间（简化版，返回固定阈值）
func (t *BrickMovingTrigger) GetOptimalThresholds() map[string]interface{} {
	return map[string]interface{}{
		"thresholdAB":  t.GetThresholdAB(),
		"thresholdBA":  t.GetThresholdBA(),
		"abTradeCount": 0,
		"baTradeCount": 0,
		"totalTrades":  0,
	}
}

// GetSlippageData 获取滑点计算结果（简化版，返回空数据）
func (t *BrickMovingTrigger) GetSlippageData() map[string]interface{} {
	return map[string]interface{}{
		"exchangeBuy":  0.0,
		"exchangeSell": 0.0,
		"onchainBuy":   0.0,
		"onchainSell":  0.0,
	}
}

// GetTelegramNotificationEnabled 获取 Telegram 通知启用状态
func (t *BrickMovingTrigger) GetTelegramNotificationEnabled() bool {
	t.telegramNotificationMu.RLock()
	defer t.telegramNotificationMu.RUnlock()
	return t.telegramNotificationEnabled
}

// SetTelegramNotificationEnabled 设置 Telegram 通知启用状态
func (t *BrickMovingTrigger) SetTelegramNotificationEnabled(enabled bool) {
	t.telegramNotificationMu.Lock()
	defer t.telegramNotificationMu.Unlock()
	t.telegramNotificationEnabled = enabled
}

// ClearPriceDiffs 清空历史价差数据
func (t *BrickMovingTrigger) ClearPriceDiffs() error {
	// BrickMovingTrigger 使用 StatisticsManager 记录价差，但 StatisticsManager 没有 ClearPriceDiffs 方法
	// 如果需要清空，可以通过其他方式实现
	t.logger.Infof("ClearPriceDiffs called for %s (not implemented)", t.symbol)
	return nil
}

// IsBundlerEnabled 检查是否启用了 Bundler（简化版，返回 false）
func (t *BrickMovingTrigger) IsBundlerEnabled() bool {
	return false
}

// EnableBundler 启用 Bundler（简化版，不支持）
func (t *BrickMovingTrigger) EnableBundler() error {
	return fmt.Errorf("BrickMovingTrigger does not support bundler")
}

// DisableBundler 禁用 Bundler（简化版，不支持）
func (t *BrickMovingTrigger) DisableBundler() error {
	return fmt.Errorf("BrickMovingTrigger does not support bundler")
}

// GetOnChainSlippage 获取链上滑点配置（兼容接口，返回 A 侧）
func (t *BrickMovingTrigger) GetOnChainSlippage() string {
	return t.GetOnChainSlippageA()
}

// SetOnChainSlippage 设置链上滑点配置（兼容接口，设置 A 侧）
func (t *BrickMovingTrigger) SetOnChainSlippage(slippage string) error {
	return t.SetOnChainSlippageA(slippage)
}

// GetOnChainSlippageA 获取 Source A 链上滑点
func (t *BrickMovingTrigger) GetOnChainSlippageA() string {
	t.onChainConfigMu.RLock()
	defer t.onChainConfigMu.RUnlock()
	if t.onChainSlippageA != "" {
		return t.onChainSlippageA
	}
	return "0.5"
}

// SetOnChainSlippageA 设置 Source A 链上滑点
func (t *BrickMovingTrigger) SetOnChainSlippageA(slippage string) error {
	t.onChainConfigMu.Lock()
	t.onChainSlippageA = slippage
	t.onChainConfigMu.Unlock()
	t.applyOnChainConfigToTrader(t.sourceA, "sourceA")
	return nil
}

// GetOnChainSlippageB 获取 Source B 链上滑点
func (t *BrickMovingTrigger) GetOnChainSlippageB() string {
	t.onChainConfigMu.RLock()
	defer t.onChainConfigMu.RUnlock()
	if t.onChainSlippageB != "" {
		return t.onChainSlippageB
	}
	return "0.5"
}

// SetOnChainSlippageB 设置 Source B 链上滑点
func (t *BrickMovingTrigger) SetOnChainSlippageB(slippage string) error {
	t.onChainConfigMu.Lock()
	t.onChainSlippageB = slippage
	t.onChainConfigMu.Unlock()
	t.applyOnChainConfigToTrader(t.sourceB, "sourceB")
	return nil
}

// GetGasMultiplier 获取链上 gas 乘数（兼容接口，返回 A 侧）
func (t *BrickMovingTrigger) GetGasMultiplier() float64 {
	return t.GetGasMultiplierA()
}

// SetGasMultiplier 设置链上 gas 乘数（兼容接口，设置 A 侧）
func (t *BrickMovingTrigger) SetGasMultiplier(multiplier float64) error {
	return t.SetGasMultiplierA(multiplier)
}

// GetGasMultiplierA 获取 Source A gas 乘数
func (t *BrickMovingTrigger) GetGasMultiplierA() float64 {
	t.onChainConfigMu.RLock()
	defer t.onChainConfigMu.RUnlock()
	if t.gasMultiplierA > 0 {
		return t.gasMultiplierA
	}
	return 1.0
}

// SetGasMultiplierA 设置 Source A gas 乘数
func (t *BrickMovingTrigger) SetGasMultiplierA(multiplier float64) error {
	t.onChainConfigMu.Lock()
	t.gasMultiplierA = multiplier
	t.onChainConfigMu.Unlock()
	t.applyOnChainConfigToTrader(t.sourceA, "sourceA")
	return nil
}

// GetGasMultiplierB 获取 Source B gas 乘数
func (t *BrickMovingTrigger) GetGasMultiplierB() float64 {
	t.onChainConfigMu.RLock()
	defer t.onChainConfigMu.RUnlock()
	if t.gasMultiplierB > 0 {
		return t.gasMultiplierB
	}
	return 1.0
}

// SetGasMultiplierB 设置 Source B gas 乘数
func (t *BrickMovingTrigger) SetGasMultiplierB(multiplier float64) error {
	t.onChainConfigMu.Lock()
	t.gasMultiplierB = multiplier
	t.onChainConfigMu.Unlock()
	t.applyOnChainConfigToTrader(t.sourceB, "sourceB")
	return nil
}

// GetOnChainGasLimit 获取链上 GasLimit（兼容接口，返回 A 侧）
func (t *BrickMovingTrigger) GetOnChainGasLimit() string {
	return t.GetOnChainGasLimitA()
}

// SetOnChainGasLimit 设置链上 GasLimit（兼容接口，设置 A 侧）
func (t *BrickMovingTrigger) SetOnChainGasLimit(gasLimit string) error {
	return t.SetOnChainGasLimitA(gasLimit)
}

// GetOnChainGasLimitA 获取 Source A 链上 GasLimit
func (t *BrickMovingTrigger) GetOnChainGasLimitA() string {
	t.onChainConfigMu.RLock()
	defer t.onChainConfigMu.RUnlock()
	if t.onChainGasLimitA != "" {
		return t.onChainGasLimitA
	}
	return "0"
}

// SetOnChainGasLimitA 设置 Source A 链上 GasLimit
func (t *BrickMovingTrigger) SetOnChainGasLimitA(gasLimit string) error {
	t.onChainConfigMu.Lock()
	t.onChainGasLimitA = gasLimit
	t.onChainConfigMu.Unlock()
	t.applyOnChainConfigToTrader(t.sourceA, "sourceA")
	return nil
}

// GetOnChainGasLimitB 获取 Source B 链上 GasLimit
func (t *BrickMovingTrigger) GetOnChainGasLimitB() string {
	t.onChainConfigMu.RLock()
	defer t.onChainConfigMu.RUnlock()
	if t.onChainGasLimitB != "" {
		return t.onChainGasLimitB
	}
	return "0"
}

// SetOnChainGasLimitB 设置 Source B 链上 GasLimit
func (t *BrickMovingTrigger) SetOnChainGasLimitB(gasLimit string) error {
	t.onChainConfigMu.Lock()
	t.onChainGasLimitB = gasLimit
	t.onChainConfigMu.Unlock()
	t.applyOnChainConfigToTrader(t.sourceB, "sourceB")
	return nil
}

// getOnChainSlippageForSource 按 source 名称返回链上滑点（供 setupOnchainForTrader 使用）
func (t *BrickMovingTrigger) getOnChainSlippageForSource(sourceName string) string {
	if sourceName == "sourceB" {
		return t.GetOnChainSlippageB()
	}
	return t.GetOnChainSlippageA()
}

// getOnChainGasLimitForSource 按 source 名称返回链上 GasLimit
func (t *BrickMovingTrigger) getOnChainGasLimitForSource(sourceName string) string {
	if sourceName == "sourceB" {
		return t.GetOnChainGasLimitB()
	}
	return t.GetOnChainGasLimitA()
}

// getOnChainGasMultiplierForSource 按 source 名称返回 gas 乘数
func (t *BrickMovingTrigger) getOnChainGasMultiplierForSource(sourceName string) float64 {
	if sourceName == "sourceB" {
		return t.GetGasMultiplierB()
	}
	return t.GetGasMultiplierA()
}

// applyOnChainConfigToTrader 将当前链上配置应用到已运行的 OnchainTrader（仅当 trader 为链上时）
func (t *BrickMovingTrigger) applyOnChainConfigToTrader(tr trader.Trader, sourceName string) {
	if tr == nil {
		return
	}
	oc, ok := tr.(*trader.OnchainTraderImpl)
	if !ok {
		return
	}
	client := oc.GetOnchainClient()
	if client == nil {
		return
	}
	if sourceName == "sourceA" {
		client.UpdateSwapInfoSlippage(t.GetOnChainSlippageA())
		client.SetGasMultiplier(t.GetGasMultiplierA())
		gl := t.GetOnChainGasLimitA()
		if gl != "" && gl != "0" {
			client.UpdateSwapInfoGasLimit(gl)
		}
	} else {
		client.UpdateSwapInfoSlippage(t.GetOnChainSlippageB())
		client.SetGasMultiplier(t.GetGasMultiplierB())
		gl := t.GetOnChainGasLimitB()
		if gl != "" && gl != "0" {
			client.UpdateSwapInfoGasLimit(gl)
		}
	}
}

// RefreshSwapInfo 用当前 trigger 配置（Amount、Slippage、GasLimit）重新设置链上端的 SwapInfo，确保 OK 等收到最新信息
func (t *BrickMovingTrigger) RefreshSwapInfo() {
	for _, item := range []struct {
		source trader.Trader
		name   string
	}{
		{t.sourceA, "sourceA"},
		{t.sourceB, "sourceB"},
	} {
		if item.source == nil {
			continue
		}
		oc, ok := item.source.(*trader.OnchainTraderImpl)
		if !ok {
			continue
		}
		client := oc.GetOnchainClient()
		if client == nil {
			continue
		}
		cur := client.GetSwapInfo()
		if cur == nil {
			continue
		}
		amount := t.GetConfiguredSizeAB()
		if amount <= 0 {
			amount = 1000
		}
		amountStr := fmt.Sprintf("%.0f", amount)
		slippage := t.getOnChainSlippageForSource(item.name)
		gasLimit := t.getOnChainGasLimitForSource(item.name)
		if gasLimit == "" || gasLimit == "0" {
			gasLimit = config.GetGlobalConfig().Onchain.DefaultGasLimit
			if gasLimit == "" {
				gasLimit = "500000"
			}
		}
		updated := *cur
		updated.Amount = amountStr
		updated.Slippage = slippage
		updated.GasLimit = gasLimit
		client.SetSwapInfo(&updated)
		t.logger.Debugf("RefreshSwapInfo: %s Amount=%s Slippage=%s GasLimit=%s", item.name, amountStr, slippage, gasLimit)
		client.SetGasMultiplier(t.getOnChainGasMultiplierForSource(item.name))
	}
}

// UpdateSwapAmountForOpen 更新链上 SwapInfo.Amount 为指定值，供套保开仓前/保存配置后同步「开仓专用数量」，避免与 Trigger 日常 Size 混用
func (t *BrickMovingTrigger) UpdateSwapAmountForOpen(amount string) {
	if amount == "" {
		return
	}
	onchainTrader := t.getOnchainTrader()
	if onchainTrader == nil {
		return
	}
	// 仅当 A 为链上时更新（开仓是 A 端买入）
	if t.sourceA != nil {
		if oc, ok := t.sourceA.(trader.OnchainTrader); ok {
			oc.UpdateSwapInfoAmount(amount)
			t.logger.Debugf("UpdateSwapAmountForOpen: symbol=%s amount=%s", t.symbol, amount)
			return
		}
	}
}

// OpenPositionOnchainA 在 A 端为链上时执行链上开仓：用 amount（USDT 数量）买入代币
// 供套保开仓接口使用，当 Trader A 为 onchain 时调用
func (t *BrickMovingTrigger) OpenPositionOnchainA(amount string) error {
	// #region agent log
	writeBMDebug("brick_moving_trigger.go:OpenPositionOnchainA", "entry", "H1", map[string]interface{}{"symbol": t.symbol, "amount": amount})
	// #endregion
	if t.sourceA == nil {
		return fmt.Errorf("sourceA 未设置")
	}
	onchainTrader, ok := t.sourceA.(trader.OnchainTrader)
	if !ok {
		return fmt.Errorf("sourceA 不是链上 trader，无法执行链上开仓")
	}
	chainIndex := t.onChainData.ChainIndex
	if chainIndex == "" && strings.HasPrefix(t.traderAType, "onchain:") {
		parts := strings.Split(t.traderAType, ":")
		if len(parts) == 2 {
			chainIndex = parts[1]
		}
	}
	if chainIndex == "" {
		chainIndex = "56"
	}
	_, _, err := onchainTrader.ExecuteSwapWithAmount(amount, chainIndex, onchain.SwapDirectionBuy)
	return err
}

// ExecuteSwapAndBroadcast 直接使用指定 amount 执行一次 swap 并广播，返回 txHash
// direction: "buy"(USDT->代币) 或 "sell"(代币->USDT)；source: "A" 或 "B"
func (t *BrickMovingTrigger) ExecuteSwapAndBroadcast(amount, direction, source string) (txHash string, err error) {
	var tr trader.Trader
	var traderType string
	switch strings.ToUpper(strings.TrimSpace(source)) {
	case "B":
		tr = t.sourceB
		traderType = t.traderBType
	default:
		tr = t.sourceA
		traderType = t.traderAType
	}
	if tr == nil {
		return "", fmt.Errorf("source %s 未设置", source)
	}
	onchainTrader, ok := tr.(trader.OnchainTrader)
	if !ok {
		return "", fmt.Errorf("source %s 不是链上 trader，无法执行 swap", source)
	}
	chainIndex := t.onChainData.ChainIndex
	if chainIndex == "" && strings.HasPrefix(traderType, "onchain:") {
		parts := strings.Split(traderType, ":")
		if len(parts) == 2 {
			chainIndex = parts[1]
		}
	}
	if chainIndex == "" {
		chainIndex = "56"
	}
	var dir onchain.SwapDirection
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "sell":
		dir = onchain.SwapDirectionSell
	default:
		dir = onchain.SwapDirectionBuy
	}
	h, _, e := onchainTrader.ExecuteSwapWithAmount(amount, chainIndex, dir)
	return h, e
}

// GetChainId 获取链ID
func (t *BrickMovingTrigger) GetChainId() string {
	return t.onChainData.ChainIndex
}

// GetExchangeType 获取交易所类型
func (t *BrickMovingTrigger) GetExchangeType() string {
	return extractExchangeType(t.traderBType)
}

// GetTraderAType 获取 A 的类型
func (t *BrickMovingTrigger) GetTraderAType() string {
	return t.traderAType
}

// GetTraderBType 获取 B 的类型
func (t *BrickMovingTrigger) GetTraderBType() string {
	return t.traderBType
}

// GetFastTriggerConfig 获取快速触发优化器配置（简化版，返回空配置）
func (t *BrickMovingTrigger) GetFastTriggerConfig() map[string]interface{} {
	return map[string]interface{}{}
}

// GetFastTriggerSpeedWeight 获取快速触发速度权重（简化版，返回默认值）
func (t *BrickMovingTrigger) GetFastTriggerSpeedWeight() float64 {
	return 0.6
}

// SetFastTriggerSpeedWeight 设置快速触发速度权重（简化版，不支持）
func (t *BrickMovingTrigger) SetFastTriggerSpeedWeight(weight float64) error {
	return fmt.Errorf("BrickMovingTrigger does not support fast trigger configuration")
}

// GetFastTriggerQuantileLevel 获取快速触发分位数水平（简化版，返回默认值）
func (t *BrickMovingTrigger) GetFastTriggerQuantileLevel() float64 {
	return 0.3
}

// SetFastTriggerQuantileLevel 设置快速触发分位数水平（简化版，不支持）
func (t *BrickMovingTrigger) SetFastTriggerQuantileLevel(level float64) error {
	return fmt.Errorf("BrickMovingTrigger does not support fast trigger configuration")
}

// GetFastTriggerMaxAcceptableDelay 获取快速触发最大可接受延迟（简化版，返回默认值）
func (t *BrickMovingTrigger) GetFastTriggerMaxAcceptableDelay() int64 {
	return 1000
}

// SetFastTriggerMaxAcceptableDelay 设置快速触发最大可接受延迟（简化版，不支持）
func (t *BrickMovingTrigger) SetFastTriggerMaxAcceptableDelay(delayMs int64) error {
	return fmt.Errorf("BrickMovingTrigger does not support fast trigger configuration")
}

// GetFastTriggerMinValidTriggers 获取快速触发最小有效触发次数（简化版，返回默认值）
func (t *BrickMovingTrigger) GetFastTriggerMinValidTriggers() int {
	return 5
}

// SetFastTriggerMinValidTriggers 设置快速触发最小有效触发次数（简化版，不支持）
func (t *BrickMovingTrigger) SetFastTriggerMinValidTriggers(count int) error {
	return fmt.Errorf("BrickMovingTrigger does not support fast trigger configuration")
}

// SetCleanupPriceDiffsInterval 设置清理价差数据间隔（简化版，不支持）
func (t *BrickMovingTrigger) SetCleanupPriceDiffsInterval(interval time.Duration) error {
	return fmt.Errorf("BrickMovingTrigger does not support cleanup interval configuration")
}

// GetCleanupPriceDiffsInterval 获取清理价差数据间隔（简化版，返回默认值）
func (t *BrickMovingTrigger) GetCleanupPriceDiffsInterval() time.Duration {
	return 5 * time.Minute
}
