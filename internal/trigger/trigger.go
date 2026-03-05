package trigger

import (
	"auto-arbitrage/internal/analytics"
	"auto-arbitrage/internal/config"
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/onchain"
	"auto-arbitrage/internal/onchain/bundler"
	"auto-arbitrage/internal/position"
	"auto-arbitrage/internal/statistics"
	"auto-arbitrage/internal/trader"
	"auto-arbitrage/internal/trigger/token_mapping"
	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/utils/parallel"
)

// OrderDirection 订单方向枚举
type OrderDirection int

const (
	DirectionAB OrderDirection = iota // +A-B 方向
	DirectionBA                       // -A+B 方向
)

// DefaultOrderSize 默认订单大小
const DefaultOrderSize = 2000.0

// String 返回方向的字符串表示
func (d OrderDirection) String() string {
	switch d {
	case DirectionAB:
		return "+A-B"
	case DirectionBA:
		return "-A+B"
	default:
		return "unknown"
	}
}

type TriggerMode int

const (
	ModeInstant   TriggerMode = iota // 即时触发
	ModeScheduled                    // 定时触发
)

// OnChainData 链上数据状态
type OnChainData struct {
	BuyTx      string // 买入交易哈希
	SellTx     string // 卖出交易哈希
	ChainIndex string // 链ID
}

// DirectionConfig 封装单个方向的所有配置和状态
type DirectionConfig struct {
	Direction             OrderDirection  // 方向标识（AB 或 BA）
	PriceData             model.PriceData // 价格数据
	LastOrderTime         time.Time       // 上次下单时间
	OrderExecutionEnabled bool            // 是否启用订单执行（false 时只计算不实际下单）
}

type Trigger struct {
	ID      uint64
	sourceA trader.Trader
	sourceB trader.Trader
	symbol  string
	mode    TriggerMode

	intervalOpt *IntervalOpt //循环间隔管理

	slippageOpt *SlippageOpt //滑点管理

	orderOpt *OrderOpt //下单管理

	// 价格数据通道：分别接收来自 sourceA 和 sourceB 的价格数据
	sourceAPriceChan chan TriggerPriceData // 接收来自 sourceA 的价格数据（可能是交易所或链上）
	sourceBPriceChan chan TriggerPriceData // 接收来自 sourceB 的价格数据（可能是交易所或链上）

	// 方向配置（使用 DirectionConfig 整合）
	directionAB *DirectionConfig // +A-B 方向配置
	directionBA *DirectionConfig // -A+B 方向配置

	// 链上数据（独立封装，不绑定方向）
	onChainData *OnChainData

	lastTicker *model.Ticker

	context context.Context
	close   context.CancelFunc

	analytics analytics.Analyzer

	backgroundTaskRoutineGroup *parallel.RoutineGroup //管理后台任务的协程组
	priceConsumerRoutineGroup  *parallel.RoutineGroup //管理价格消费协程的协程组
	logger                     *zap.SugaredLogger

	// 状态管理
	isRunning bool
	statusMu  sync.RWMutex

	// 执行中锁：防止同一方向并发执行（冷却在 executeOrderV2 返回后才更新，执行期间多个 tick 可能同时通过冷却检查）
	executingAB   bool
	executingBA   bool
	executingMu   sync.Mutex

	// Telegram 通知配置
	telegramNotificationEnabled bool
	telegramNotificationMu      sync.RWMutex

	// 类型信息（从映射表或请求中获取）
	traderAType string // 如 "onchain:56"
	traderBType string // 如 "binance:futures"
}

// TriggerPriceData Trigger所需要的价格数据 内嵌 ExchangePriceMsg OnChainPriceMsg
type TriggerPriceData struct {
	ExchangePriceMsg
	OnChainPriceMsg
}

func (p TriggerPriceData) PrintLog() string {
	return fmt.Sprintf("ExchangePriceMsg{symbol:%s, ticker:%v}", p.symbol, p.ticker.PrintLog())
}


// subscribeOnchainForSource 为指定的 source（A 或 B）订阅链上价格
// isSourceA: true 表示订阅 sourceA，false 表示订阅 sourceB
func (t *Trigger) subscribeOnchainForSource(symbol string, isSourceA bool) error {
	okOnChainClient := onchain.NewOkdex()
	okOnChainClient.Init()

	// 根据 isSourceA 确定 traderType
	var traderType string
	if isSourceA {
		traderType = t.traderAType
	} else {
		traderType = t.traderBType
	}
	if traderType == "" {
		// 默认使用 onchain:56
		traderType = "onchain:56"
	}

	// 创建 OnchainTrader 适配器
	onchainTrader := trader.NewOnchainTrader(okOnChainClient, traderType)

	// 根据 isSourceA 创建对应的回调函数，写入对应的 channel
	var targetChan chan TriggerPriceData
	var sourceName string
	if isSourceA {
		targetChan = t.sourceAPriceChan
		sourceName = "sourceA"
	} else {
		targetChan = t.sourceBPriceChan
		sourceName = "sourceB"
	}

	// 设置价格回调，写入对应的 channel
	onchainTrader.SetPriceCallback(func(symbol string, priceData trader.PriceData) {
		if priceData.ChainPrice == nil {
			return
		}
		price := priceData.ChainPrice
		// 保存链上交易数据
		if price.ChainBuyTx != "" {
			t.onChainData.BuyTx = price.ChainBuyTx
		}
		if price.ChainSellTx != "" {
			t.onChainData.SellTx = price.ChainSellTx
		}
		if price.ChainId != "" {
			t.onChainData.ChainIndex = price.ChainId
		}

		// 写入对应的 channel
		select {
		case targetChan <- TriggerPriceData{
			OnChainPriceMsg: OnChainPriceMsg{
				price: price,
			},
		}:
		default:
			t.logger.Warnf("%s price channel full, dropping onchain price", sourceName)
		}
	})

	// 设置 bundler（如果配置了）
	if config.GetGlobalConfig().Bundler.UseBundler {
		bundlerMgr := setupBundler()
		if bundlerMgr != nil {
			onchain.SetBundlerForClient(okOnChainClient, bundlerMgr, true)
			t.logger.Infof("Bundler enabled for onchain trader (bundlers: %d)", len(bundlerMgr.GetAllBundlers()))
		} else {
			t.logger.Warnf("Bundler is enabled but no bundler configured (check FlashbotsPrivateKey or FortyEightClubAPIKey)")
		}
	}

	// 从 symbol 中提取目标代币符号（例如：从 "RAVEUSDT" 提取 "RAVE"）
	// 假设交易对格式为 {TOKEN}USDT
	toTokenSymbol := strings.TrimSuffix(symbol, "USDT")
	if toTokenSymbol == symbol {
		// 如果没有 USDT 后缀，尝试其他常见后缀
		toTokenSymbol = strings.TrimSuffix(symbol, "USDC")
		if toTokenSymbol == symbol {
			toTokenSymbol = strings.TrimSuffix(symbol, "BUSD")
		}
	}

	// 获取链ID，优先使用 onChainData.ChainIndex，如果未设置则使用默认值 "56"
	chainId := "56" // 默认 BSC 链
	if t.onChainData != nil && t.onChainData.ChainIndex != "" {
		chainId = t.onChainData.ChainIndex
	}

	// 获取 TokenMappingManager
	mappingMgr := token_mapping.GetTokenMappingManager()

	// 默认 USDT 地址（BSC）
	fromTokenSymbol := "USDT"
	fromTokenAddress := "0x55d398326f99059ff775485246999027b3197955"
	fromTokenDecimals := "18"

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

	amountStr := fmt.Sprintf("%.0f", DefaultOrderSize)
	// 从配置获取 GasLimit，默认 "500000"
	gasLimit := config.GetGlobalConfig().Onchain.DefaultGasLimit
	if gasLimit == "" {
		gasLimit = "500000"
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
		Slippage:                 "0.2",
		GasLimit:                 gasLimit,
		WalletAddress:            config.GetGlobalConfig().Wallet.WalletAddress,
	}

	t.logger.Infof("初始化链上订阅 [%s]: %s -> %s (%s -> %s)", sourceName, fromTokenSymbol, toTokenSymbol, fromTokenAddress, toTokenAddress)

	// 设置 swapInfo 并启动 swap
	onchainTrader.SetSwapInfo(swapInfo)
	onchainTrader.StartSwap(swapInfo)

	// 设置 sourceA 或 sourceB
	if isSourceA {
		t.sourceA = onchainTrader
	} else {
		t.sourceB = onchainTrader
	}

	// 🔥 立即注册到 PositionManager（使用 OnchainTrader）
	positionManager := position.GetPositionManager()
	if positionManager != nil {
		positionManager.RegisterOnchainTrader(t.symbol, onchainTrader)
		// 注册 trader 类型，供 TriggerImmediateUpdate -> GetSize 使用，避免 "trader type is empty"
		positionManager.RegisterTraderTypes(t.symbol, t.traderAType, t.traderBType)
		t.logger.Infof("已立即注册 OnchainTrader 到 PositionManager: symbol=%s, source=%s", t.symbol, sourceName)

		// 立即触发一次 swapInfo.Amount 更新
		positionManager.TriggerImmediateUpdate(t.symbol)
		t.logger.Debugf("已触发立即更新 swapInfo.Amount: symbol=%s", t.symbol)
	} else {
		t.logger.Warnf("PositionManager 未初始化，无法注册 OnchainTrader: symbol=%s, source=%s", t.symbol, sourceName)
	}

	return nil
}

// getOnchainTrader 获取链上 Trader（从 sourceA 或 sourceB）
func (t *Trigger) getOnchainTrader() trader.OnchainTrader {
	if t.sourceA != nil {
		if onchain, ok := t.sourceA.(trader.OnchainTrader); ok {
			return onchain
		}
	}
	if t.sourceB != nil {
		if onchain, ok := t.sourceB.(trader.OnchainTrader); ok {
			return onchain
		}
	}
	return nil
}


// setupBundler 设置 bundler（如果配置了的话）
func setupBundler() *bundler.Manager {
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

// NewTriggerWithMode 创建单个交易对的触发器（使用 TriggerMode 类型）
func (tm *TriggerManager) NewTriggerWithMode(symbol string, sourceA, sourceB trader.Trader, mode TriggerMode) *Trigger {
	tg := &Trigger{
		ID:      tm.idGen.NextId(),
		sourceA: sourceA,
		sourceB: sourceB,
		symbol:  symbol,
		mode:    mode,

		intervalOpt: defaultIntervalOpt(),

		slippageOpt: defaultSlippageOpt(),

		orderOpt: defaultOrderOpt(),

		sourceAPriceChan: make(chan TriggerPriceData, 2048),
		sourceBPriceChan: make(chan TriggerPriceData, 2048),

		// 初始化方向配置
		directionAB: &DirectionConfig{
			Direction:             DirectionAB,
			PriceData:             model.PriceData{},
			LastOrderTime:         time.Time{},
			OrderExecutionEnabled: false, // 默认不启用订单执行
		},
		directionBA: &DirectionConfig{
			Direction:             DirectionBA,
			PriceData:             model.PriceData{},
			LastOrderTime:         time.Time{},
			OrderExecutionEnabled: false, // 默认不启用订单执行
		},

		// 初始化链上数据
		onChainData: &OnChainData{
			BuyTx:      "",
			SellTx:     "",
			ChainIndex: "",
		},

		analytics: analytics.NewAnalytics(config.GetGlobalConfig().Arbitrage.DefaultTargetThresholdInterval, 3000, symbol),

		backgroundTaskRoutineGroup: parallel.NewRoutineGroup(),
		priceConsumerRoutineGroup:  parallel.NewRoutineGroup(),
		logger:                     logger.GetLoggerInstance().Named(fmt.Sprintf("tg_%s", symbol)).Sugar(),

		// Telegram 通知默认启用
		telegramNotificationEnabled: true,
	}

	tg.context, tg.close = context.WithCancel(tm.triggerContext)

	// 设置 Analytics 的通知回调函数，用于检查是否应该发送 Telegram 通知
	if v, ok := tg.analytics.(*analytics.Analytics); ok {
		v.SetNotificationCallback(func() bool {
			return tg.GetTelegramNotificationEnabled()
		})
	}

	return tg
}

// StartBackgroundTask 启动后台任务
func (t *Trigger) StartBackgroundTask(ctx context.Context) {
	//这里是一堆后台任务（非下单相关逻辑）
	// 注意：后台任务内部使用的是 t.context，所以传入的 ctx 参数实际上没有被使用
	// 但为了保持接口一致性，仍然保留 ctx 参数
	loopTasks := []func(){
		t.startCalcPriceDiff,
		t.startCalcSlippage,
		t.startCalcOptimalThresholds,
		t.startCleanupPriceDiffs,
		t.startTradeStalenessCheck,    // 检查成交停滞，超过30秒无成交则重置阈值数据
		t.startPositionBalanceCheck,    // 检查仓位平衡，每1分钟检查一次，偏差超过10%则自动平衡
	}
	t.backgroundTaskRoutineGroup.Parallel(loopTasks...)
}

// Start 启动套利
func (t *Trigger) Start(ctx context.Context) error {
	t.logger.Infof("启动 Trigger for symbol: %s", t.symbol)

	// 检查是否已经在运行
	t.statusMu.Lock()
	if t.isRunning {
		t.statusMu.Unlock()
		t.logger.Warnf("Trigger %s 已经在运行中", t.symbol)
		return fmt.Errorf("trigger %s is already running", t.symbol)
	}

	// 检查 context 是否已被取消，如果是则重新创建
	// 这通常发生在 Stop() 后再次 Start() 的情况
	if t.context != nil && t.context.Err() != nil {
		t.logger.Infof("Trigger %s 的 context 已被取消，正在重新创建...", t.symbol)
		t.context, t.close = context.WithCancel(ctx)
		t.logger.Infof("Trigger %s 的 context 已重新创建", t.symbol)
	}

	// 重新创建后台任务组（因为之前的任务组已经在 Stop() 时等待完成）
	t.backgroundTaskRoutineGroup = parallel.NewRoutineGroup()
	t.logger.Debugf("Trigger %s 的后台任务组已重新创建", t.symbol)

	// 重新创建价格消费协程组（因为之前的协程组已经在 Stop() 时等待完成）
	t.priceConsumerRoutineGroup = parallel.NewRoutineGroup()
	t.logger.Debugf("Trigger %s 的价格消费协程组已重新创建", t.symbol)

	t.isRunning = true
	t.statusMu.Unlock()

	// 注册到 PositionManager
	t.registerToPositionManager()

	// 启动价格消费协程（必须在后台任务之前启动，确保能接收价格消息）
	t.priceConsumerRoutineGroup.GoSafe(func() {
		t.startConsumePriceMsg()
	})

	// 启动后台循环任务（使用 t.context，这样 Stop() 可以正确停止）
	t.StartBackgroundTask(t.context)

	// 启动下单循环
	parallel.GoSafe(func() {
		t.orderAB(t.context)
	})
	parallel.GoSafe(func() {
		t.orderBA(t.context)
	})

	t.logger.Infof("Trigger %s 启动成功", t.symbol)
	return nil
}

// orderAB +A-B 方向的订单触发循环
func (t *Trigger) orderAB(ctx context.Context) {
	ticker := time.NewTicker(t.intervalOpt.orderLoop)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			parallel.RunSafe(func() {
				t.checkAndExecuteOrderABV2()
			})
		}
	}
}

// orderBA -A+B 方向的订单触发循环
func (t *Trigger) orderBA(ctx context.Context) {
	ticker := time.NewTicker(t.intervalOpt.orderLoop)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			parallel.RunSafe(func() {
				t.checkAndExecuteOrderBAV2()
			})
		}
	}
}

// Stop 停止套利
func (t *Trigger) Stop() error {
	t.statusMu.Lock()
	if !t.isRunning {
		t.statusMu.Unlock()
		t.logger.Warnf("Trigger %s 未在运行", t.symbol)
		return fmt.Errorf("trigger %s is not running", t.symbol)
	}
	t.isRunning = false
	t.statusMu.Unlock()

	t.logger.Infof("正在停止 Trigger %s...", t.symbol)

	// 先停止链上询价循环，避免 goroutine 常驻
	if onchainTrader := t.getOnchainTrader(); onchainTrader != nil {
		onchainTrader.StopSwap()
	}

	// 从 PositionManager 取消注册
	t.unregisterFromPositionManager()

	// 取消 context，这会停止所有使用 t.context 的后台任务
	if t.close != nil {
		t.close()
		t.logger.Infof("Trigger %s 的 context 已取消", t.symbol)
	}

	// 等待价格消费协程组完成
	if t.priceConsumerRoutineGroup != nil {
		t.priceConsumerRoutineGroup.Wait()
		t.logger.Infof("Trigger %s 的价格消费协程已停止", t.symbol)
	}

	// 等待后台任务组完成
	if t.backgroundTaskRoutineGroup != nil {
		t.backgroundTaskRoutineGroup.Wait()
		t.logger.Infof("Trigger %s 的后台任务已停止", t.symbol)
	}

	t.logger.Infof("Trigger %s 已成功停止", t.symbol)
	return nil
}

// registerToPositionManager 注册到 PositionManager
func (t *Trigger) registerToPositionManager() {
	positionManager := position.GetPositionManager()
	if positionManager == nil {
		t.logger.Warn("PositionManager 未初始化，跳过注册")
		return
	}

	// 注册 symbol
	positionManager.RegisterSymbol(t.symbol)

	// 注册 Analytics
	if t.analytics != nil {
		positionManager.RegisterAnalytics(t.symbol, t.analytics)
		t.logger.Infof("已注册到 PositionManager: symbol=%s", t.symbol)
	} else {
		t.logger.Warnf("Analytics 为空，无法注册到 PositionManager: symbol=%s", t.symbol)
	}

	// 注册 Onchain Trader（从 sourceA 或 sourceB 获取）
	if onchainTrader := t.getOnchainTrader(); onchainTrader != nil {
		positionManager.RegisterOnchainTrader(t.symbol, onchainTrader)
		t.logger.Debugf("已确认 onchain trader 注册到 PositionManager: symbol=%s", t.symbol)
	}

	// 注册 trader 类型，供 updateSwapInfoAmountForSymbol 等调用 GetSize 时使用，避免 "trader type is empty"
	positionManager.RegisterTraderTypes(t.symbol, t.traderAType, t.traderBType)

	// 立即触发一次 swapInfo.Amount 更新（不等待定时任务）
	positionManager.TriggerImmediateUpdate(t.symbol)

	// 注册到 StatisticsManager
	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager != nil {
		statisticsManager.RegisterSymbol(t.symbol)
		t.logger.Infof("已注册到 StatisticsManager: symbol=%s", t.symbol)
	} else {
		t.logger.Warn("StatisticsManager 未初始化，跳过注册")
	}
}

// unregisterFromPositionManager 从 PositionManager 取消注册
func (t *Trigger) unregisterFromPositionManager() {
	positionManager := position.GetPositionManager()
	if positionManager == nil {
		return
	}

	// 取消注册 Onchain Trader
	positionManager.UnregisterOnchainTrader(t.symbol)

	positionManager = position.GetPositionManager()
	if positionManager == nil {
		return
	}

	// 先取消 symbol 和 Analytics，避免定时任务仍用该 symbol 调 GetSize 时拿到已清的 trader 类型
	positionManager.UnregisterSymbol(t.symbol)
	positionManager.UnregisterAnalytics(t.symbol)
	positionManager.UnregisterTraderTypes(t.symbol)
	t.logger.Infof("已从 PositionManager 取消注册: symbol=%s", t.symbol)

	// 从 StatisticsManager 取消注册
	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager != nil {
		statisticsManager.UnregisterSymbol(t.symbol)
		t.logger.Infof("已从 StatisticsManager 取消注册: symbol=%s", t.symbol)
	}
}

// updatePriceCacheToPositionManager 更新价格缓存到 PositionManager
func (t *Trigger) updatePriceCacheToPositionManager() {
	positionManager := position.GetPositionManager()
	if positionManager == nil {
		return
	}

	// 构建综合价格数据（包含交易所和链上价格）
	// 使用交易所价格作为主要价格（因为交易所价格更稳定和可靠）
	priceData := &model.PriceData{
		BidPrice: t.directionAB.PriceData.BidPrice, // 交易所 Bid
		AskPrice: t.directionBA.PriceData.AskPrice, // 交易所 Ask
	}

	// 只有当价格有效时才更新
	if priceData.BidPrice > 0 && priceData.AskPrice > 0 {
		positionManager.UpdatePrice(t.symbol, priceData)
	}
}

// startConsumePriceMsg 开始消费价格消息队列
// 现在直接从 sourceA 和 sourceB 获取价格数据
func (t *Trigger) startConsumePriceMsg() {
	// 为 sourceA 和 sourceB 设置价格回调
	t.setupPriceCallbacks()
	
	// 从 channel 接收价格数据（回调函数会将数据写入这些 channel）
	for {
		select {
		case <-t.context.Done():
			t.logger.Debugf("[价格消费] Trigger %s 的 context 已取消，退出价格消费循环", t.symbol)
			return
		case priceData := <-t.sourceAPriceChan:
			// 处理来自 sourceA 的价格数据
			// 在处理数据前再次检查 context，避免在停止时继续处理
			if t.context.Err() != nil {
				t.logger.Debugf("[价格消费] Trigger %s 的 context 已取消，停止处理价格数据", t.symbol)
				return
			}
			t.handleSourceAPrice(priceData)
		case priceData := <-t.sourceBPriceChan:
			// 处理来自 sourceB 的价格数据
			// 在处理数据前再次检查 context，避免在停止时继续处理
			if t.context.Err() != nil {
				t.logger.Debugf("[价格消费] Trigger %s 的 context 已取消，停止处理价格数据", t.symbol)
				return
			}
			t.handleSourceBPrice(priceData)
		}
	}
}

// handleSourceAPrice 处理来自 sourceA 的价格数据
func (t *Trigger) handleSourceAPrice(priceData TriggerPriceData) {
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
func (t *Trigger) handleSourceBPrice(priceData TriggerPriceData) {
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

// setupPriceCallbacks 为 sourceA 和 sourceB 设置价格回调
// 仅对 OnchainTrader 调用 SetPriceCallback；CEX/DEX 的价格由 handlePriceMsgChan 经 exchangePriceMsgChan 按 exchangeType 路由到 sourceXPriceChan，不再经 Trader.SetPriceCallback，避免单个 CEX/DEX 只能存一个回调导致其他 trigger 收不到。
func (t *Trigger) setupPriceCallbacks() {
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
				// 链上回调传入的 symbol 多为 CoinSymbol（如 RAVE），与 t.symbol（RAVEUSDT）常不一致；
				// 链上 Trader 与 Swap 一对一，此处不做 symbol 过滤，避免 token 映射创建的 trigger 收不到链上价导致价差无法计算
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
				select { case <-targetChan: default: }
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

// startCalcPriceDiff 开始循环计算价差 丢给分析器模块
func (t *Trigger) startCalcPriceDiff() {
	tick := time.NewTicker(t.intervalOpt.calcPriceDiff)
	defer tick.Stop()

	for {
		select {
		case <-t.context.Done():
			return
		case <-tick.C:
			parallel.RunSafe(func() {
				if t.directionBA.PriceData.BidPrice == 0 || t.directionBA.PriceData.AskPrice == 0 ||
					t.directionAB.PriceData.BidPrice == 0 || t.directionAB.PriceData.AskPrice == 0 {
					return
				}

				// 计算并设置动态成本（在 OnPriceDiff 之前）
				t.updateCurrentCosts()

				diff, err := CalculateDiff(&t.directionAB.PriceData, &t.directionBA.PriceData)
				if err != nil {
					t.logger.Errorf("CalculateDiff err:%v", err)
					return
				}
				t.analytics.OnPriceDiff(diff)

				// 记录价差数据到 StatisticsManager
				statisticsManager := statistics.GetStatisticsManager()
				if statisticsManager != nil {
					statisticsManager.RecordPriceDiff(t.symbol, diff.DiffAB, diff.DiffBA)
				}
			})
		}
	}
}

// updateCurrentCosts 计算并设置两个方向的动态成本到 analytics
// 用于快速触发优化器判断净利润
func (t *Trigger) updateCurrentCosts() {
	// 构建 A 的价格数据
	priceDataA := &model.PriceData{
		AskPrice: t.directionAB.PriceData.AskPrice, // A 的 Ask（卖一价）
		BidPrice: t.directionBA.PriceData.BidPrice, // A 的 Bid（买一价）
	}

	// 构建 B 的价格数据
	priceDataB := &model.PriceData{
		AskPrice: t.directionBA.PriceData.AskPrice, // B 的 Ask（卖一价）
		BidPrice: t.directionAB.PriceData.BidPrice, // B 的 Bid（买一价）
	}

	// 使用默认 size 计算成本
	size := DefaultOrderSize

	// 类型断言获取 Analytics 实例
	analyticsInstance, ok := t.analytics.(*analytics.Analytics)
	if !ok {
		return
	}

	// 计算 AB 方向成本（+A-B：A买入 + B卖出）
	// CalculateCostForDirection 参数：(direction, size, B的价格, A的价格)
	costDataAB := analyticsInstance.CalculateCostForDirection("AB", size, priceDataB, priceDataA)
	var costAB float64
	if costDataAB != nil {
		costAB = costDataAB.TotalCostPercent
	}

	// 计算 BA 方向成本（-A+B：A卖出 + B买入）
	costDataBA := analyticsInstance.CalculateCostForDirection("BA", size, priceDataB, priceDataA)
	var costBA float64
	if costDataBA != nil {
		costBA = costDataBA.TotalCostPercent
	}

	// 设置到 analytics
	analyticsInstance.SetCurrentCosts(costAB, costBA)
}

// startCalcSlippage 开始循环计算滑点
func (t *Trigger) startCalcSlippage() {
	tick := time.NewTicker(t.intervalOpt.calcSlippage)
	defer tick.Stop()

	for {
		select {
		case <-t.context.Done():
			return
		case <-tick.C:
			parallel.RunSafe(func() {
				t.calculateSlippage()
			})
		}
	}
}

// startCalcOptimalThresholds 开始循环计算最优阈值
func (t *Trigger) startCalcOptimalThresholds() {
	tick := time.NewTicker(t.intervalOpt.calcOptimalThresholds)
	defer tick.Stop()

	for {
		select {
		case <-t.context.Done():
			return
		case <-tick.C:
			parallel.RunSafe(func() {
				if v, ok := t.analytics.(*analytics.Analytics); ok {
					v.CalculateOptimalThresholds()
				}
			})
		}
	}
}

// startCleanupPriceDiffs 开始定时清理价差数据
func (t *Trigger) startCleanupPriceDiffs() {
	// 如果清理间隔为 0，表示不自动清理
	if t.intervalOpt.cleanupPriceDiffs == 0 {
		return
	}

	tick := time.NewTicker(t.intervalOpt.cleanupPriceDiffs)
	defer tick.Stop()

	for {
		select {
		case <-t.context.Done():
			return
		case <-tick.C:
			parallel.RunSafe(func() {
				if err := t.ClearPriceDiffs(); err != nil {
					t.logger.Errorf("定时清理价差数据失败: %v", err)
				} else {
					t.logger.Infof("定时清理价差数据成功，清理间隔: %v", t.intervalOpt.cleanupPriceDiffs)
				}
			})
		}
	}
}

// startTradeStalenessCheck 开始检查成交停滞（超过3分钟无成交则重置阈值数据）
// 解决阈值计算持续一段时间后固定的问题
// 重要：清空后需要等待计算出有效阈值才能再次清空，避免持续清空导致阈值无法计算
func (t *Trigger) startTradeStalenessCheck() {
	const checkInterval = 30 * time.Second       // 检查间隔：30秒
	const stalenessThreshold = 3 * time.Minute   // 停滞阈值：3分钟无成交
	tick := time.NewTicker(checkInterval)
	defer tick.Stop()

	// 🔍 启动日志：确认任务已启动
	t.logger.Infof("成交停滞检查 [%s]: 任务已启动，检查间隔=%v，停滞阈值=%v", t.symbol, checkInterval, stalenessThreshold)

	checkCount := 0 // 检查计数器，用于周期性输出状态

	for {
		select {
		case <-t.context.Done():
			t.logger.Infof("成交停滞检查 [%s]: 任务已停止（context done）", t.symbol)
			return
		case <-tick.C:
			parallel.RunSafe(func() {
				checkCount++

				// 检查 analytics 是否可用
				if t.analytics == nil {
					t.logger.Warnf("成交停滞检查 [%s] #%d: Analytics 未初始化，跳过", t.symbol, checkCount)
					return
				}

				v, ok := t.analytics.(*analytics.Analytics)
				if !ok {
					t.logger.Warnf("成交停滞检查 [%s] #%d: Analytics 类型断言失败，跳过", t.symbol, checkCount)
					return
				}

				lastTradeTime := v.GetLastTradeTime()
				now := time.Now()

				// lastTradeTime 为零值表示尚未有成交记录，跳过
				if lastTradeTime.IsZero() {
					// 每 2 次（约 1 分钟）输出一次，避免日志过多
					if checkCount%2 == 1 {
						t.logger.Debugf("成交停滞检查 [%s] #%d: lastTradeTime 为零值（尚未有成交记录），跳过清空检查", t.symbol, checkCount)
					}
					return
				}

				// 检查当前是否有有效阈值
				// 如果没有有效阈值（optimalThresholds 为 nil 或 TotalTrades == 0），说明正在等待计算，不应该清空
				optimalThresholds := v.GetOptimalThresholds()
				if optimalThresholds == nil || optimalThresholds.TotalTrades == 0 {
					// 每 2 次（约 1 分钟）输出一次
					if checkCount%2 == 1 {
						t.logger.Debugf("成交停滞检查 [%s] #%d: 当前无有效阈值（等待计算中），跳过清空检查", t.symbol, checkCount)
					}
					return
				}

				timeSinceLastTrade := now.Sub(lastTradeTime)

				// 只有当超过阈值时才输出日志
				if timeSinceLastTrade >= stalenessThreshold {
					t.logger.Warnf("成交停滞检测 [%s] #%d: 🔥 触发清空！距离上次成交已过去 %v（>= %v）",
						t.symbol, checkCount, timeSinceLastTrade.Round(time.Second), stalenessThreshold)

					// 使用 Trigger.ClearPriceDiffs() 方法，与 web 接口保持一致
					if err := t.ClearPriceDiffs(); err != nil {
						t.logger.Errorf("成交停滞检测 [%s] #%d: 清空价差数据失败: %v", t.symbol, checkCount, err)
					} else {
						t.logger.Infof("成交停滞检测 [%s] #%d: ✅ 阈值数据已重置，等待重新计算有效阈值后才会再次检查", t.symbol, checkCount)
					}
				}
			})
		}
	}
}

// getPositionSizes 获取 A 和 B 的仓位大小
// 返回: (positionA, positionB, error)
// positionA: 链上余额（TotalOnchainBalance，正数）
// positionB: 交易所净持仓（TotalExchangeLongSize - TotalExchangeShortSize，可能为负）
func (t *Trigger) getPositionSizes() (float64, float64, error) {
	positionManager := position.GetPositionManager()
	if positionManager == nil {
		return 0, 0, fmt.Errorf("PositionManager 未初始化")
	}

	summary := positionManager.GetSymbolPositionSummary(t.symbol)
	if summary == nil {
		return 0, 0, fmt.Errorf("无法获取 %s 的仓位汇总", t.symbol)
	}

	// A 的仓位 = 链上余额（正数）
	positionA := summary.TotalOnchainBalance

	// B 的仓位 = 交易所净持仓（多头 - 空头，可能为负）
	positionB := summary.TotalExchangeLongSize - summary.TotalExchangeShortSize

	return positionA, positionB, nil
}

// checkPositionBalance 检查仓位是否平衡
// 返回: (needsBalance, positionA, positionB, adjustSize, adjustSide, error)
// needsBalance: 是否需要平衡
// adjustSize: 需要调整的数量（正数）
// adjustSide: 需要调整的一边（"A" 或 "B"）
func (t *Trigger) checkPositionBalance() (bool, float64, float64, float64, string, error) {
	positionA, positionB, err := t.getPositionSizes()
	if err != nil {
		return false, 0, 0, 0, "", err
	}

	// 计算绝对值用于比较
	absA := math.Abs(positionA)
	absB := math.Abs(positionB)

	// 如果两边都是 0，不需要平衡
	if absA == 0 && absB == 0 {
		return false, positionA, positionB, 0, "", nil
	}

	// 计算差值：abs(positionA - abs(positionB))
	diff := math.Abs(positionA - absB)

	// 计算偏差百分比：diff / max(abs(positionA), abs(positionB))
	maxSize := math.Max(absA, absB)
	if maxSize == 0 {
		return false, positionA, positionB, 0, "", nil
	}

	deviation := diff / maxSize
	const threshold = 0.1 // 10% 阈值

	if deviation <= threshold {
		// 偏差在阈值内，不需要平衡
		return false, positionA, positionB, 0, "", nil
	}

	// 偏差超过 10%，需要平衡
	var adjustSize float64
	var adjustSide string

	if positionA > absB {
		// A 多：A 需要卖出差额
		adjustSize = positionA - absB
		adjustSide = "A"
	} else if absB > positionA {
		// B 多：B 需要平仓差额
		adjustSize = absB - positionA
		adjustSide = "B"
	} else {
		// 理论上不应该到这里，但为了安全返回 false
		return false, positionA, positionB, 0, "", nil
	}

	t.logger.Warnf("仓位不平衡检测: A=%.6f, B=%.6f, 差值=%.6f, 偏差=%.2f%%, 需要调整: %s 减少 %.6f",
		positionA, positionB, diff, deviation*100, adjustSide, adjustSize)

	return true, positionA, positionB, adjustSize, adjustSide, nil
}

// executePositionBalance 执行仓位平衡操作
func (t *Trigger) executePositionBalance(adjustSize float64, adjustSide string) error {
	if adjustSize <= 0 {
		return fmt.Errorf("调整数量无效: %.6f", adjustSize)
	}

	// 对调整数量进行精度处理，截断到整数（避免 Binance 精度错误）
	// 大多数代币要求整数数量，小数会导致 "Precision is over the maximum defined for this asset" 错误
	adjustSize = math.Floor(adjustSize)
	if adjustSize <= 0 {
		return fmt.Errorf("调整数量截断后为 0，跳过平衡")
	}

	positionA, positionB, err := t.getPositionSizes()
	if err != nil {
		return fmt.Errorf("获取仓位失败: %w", err)
	}

	t.logger.Infof("开始执行仓位平衡: %s 减少 %.0f (当前 A=%.6f, B=%.6f)", adjustSide, adjustSize, positionA, positionB)

	// 检查余额是否足够
	if adjustSide == "A" {
		// A 卖出：检查链上余额是否足够
		if positionA < adjustSize {
			return fmt.Errorf("链上余额不足: 当前=%.6f, 需要=%.6f", positionA, adjustSize)
		}
		// 执行 A 卖出（链上卖出）
		if t.sourceA == nil {
			return fmt.Errorf("sourceA 不可用")
		}
		onchainTrader, ok := t.sourceA.(trader.OnchainTrader)
		if !ok {
			return fmt.Errorf("sourceA 不是链上 trader，无法执行平衡")
		}

		chainIndex := t.onChainData.ChainIndex
		if chainIndex == "" {
			chainIndex = "56" // 默认 BSC
		}

		// 使用独立执行方法，临时指定 amount，不影响全局 SwapInfo
		amountStr := strconv.FormatFloat(adjustSize, 'f', 0, 64)
		t.logger.Debugf("仓位平衡：使用独立 swap 执行，amount=%s (%.6f)", amountStr, adjustSize)

		// 执行链上卖出（使用独立方法，不影响全局 SwapInfo）
		txHash, onchainResult, err := onchainTrader.ExecuteSwapWithAmount(amountStr, chainIndex, onchain.SwapDirectionSell)
		if err != nil {
			return fmt.Errorf("A 卖出失败: %w", err)
		}

		actualAmount := float64(0)
		if onchainResult != nil {
			actualAmount = onchainResult.CoinAmount
		}

		t.logger.Infof("✅ 仓位平衡完成: A 卖出 %.6f (txHash=%s)", actualAmount, txHash)

		// 刷新余额
		positionManager := position.GetPositionManager()
		if positionManager != nil {
			go func() {
				positionManager.ForceRefreshAndUpdate(t.symbol)
			}()
		}

	} else {
		// B 平仓：检查交易所持仓是否足够
		absB := math.Abs(positionB)
		if absB < adjustSize {
			return fmt.Errorf("交易所持仓不足: 当前=%.6f, 需要=%.6f", absB, adjustSize)
		}

		// 执行 B 平仓（交易所平仓）
		if t.sourceB == nil {
			return fmt.Errorf("sourceB 不可用")
		}

		// 确定平仓方向
		var side model.OrderSide
		if positionB > 0 {
			// 多头持仓，需要卖出平仓
			side = model.OrderSideSell
		} else {
			// 空头持仓，需要买入平仓
			side = model.OrderSideBuy
		}

		// 构建平仓订单请求
		orderReq := &model.PlaceOrderRequest{
			Symbol:     t.symbol,
			Side:       side,
			Type:       model.OrderTypeMarket,
			Quantity:   adjustSize,
			MarketType: model.MarketTypeFutures,
			ReduceOnly: true, // 只减仓
		}

		order, err := t.sourceB.ExecuteOrder(orderReq)
		if err != nil {
			return fmt.Errorf("B 平仓失败: %w", err)
		}

		actualAmount := float64(0)
		orderID := ""
		if order != nil {
			if order.FilledQty > 0 {
				actualAmount = order.FilledQty
			}
			orderID = order.OrderID
		}

		t.logger.Infof("✅ 仓位平衡完成: B 平仓 %.6f (OrderID=%s)", actualAmount, orderID)

		// 刷新余额
		positionManager := position.GetPositionManager()
		if positionManager != nil {
			go func() {
				positionManager.ForceRefreshAndUpdate(t.symbol)
			}()
		}
	}

	return nil
}

// startPositionBalanceCheck 开始定时检查仓位平衡（每1分钟）
func (t *Trigger) startPositionBalanceCheck() {
	tick := time.NewTicker(1 * time.Minute)
	defer tick.Stop()

	for {
		select {
		case <-t.context.Done():
			return
		case <-tick.C:
			parallel.RunSafe(func() {
				needsBalance, positionA, positionB, adjustSize, adjustSide, err := t.checkPositionBalance()
				if err != nil {
					t.logger.Debugf("仓位平衡检查失败: %v", err)
					return
				}

				if !needsBalance {
					t.logger.Debugf("仓位平衡检查: A=%.6f, B=%.6f, 偏差在阈值内，无需平衡", positionA, positionB)
					return
				}

				// 执行平衡操作
				if err := t.executePositionBalance(adjustSize, adjustSide); err != nil {
					t.logger.Errorf("仓位平衡执行失败: %v", err)
					// 不阻塞后续检查，记录错误即可
				}
			})
		}
	}
}

// calculateSlippage 计算滑点
// 统一使用 CalculateSlippage 方法，内部会通过 CalculateTraderSlippage 判断是交易所还是链上，走不同的分支
func (t *Trigger) calculateSlippage() {
	slippageLimit := t.slippageOpt.limit
	
	// 根据当前价格计算 amount（最大限制 2000 USDT，与 size 最大限制一致）
	// 使用交易所的中间价作为基准价格
	var currentPrice float64
	if t.lastTicker != nil && t.lastTicker.LastPrice > 0 {
		currentPrice = t.lastTicker.LastPrice
	} else if t.directionAB.PriceData.BidPrice > 0 && t.directionBA.PriceData.AskPrice > 0 {
		// 使用交易所的中间价（B 的 Bid 和 Ask 的平均值）
		currentPrice = (t.directionAB.PriceData.BidPrice + t.directionBA.PriceData.AskPrice) / 2.0
	} else {
		// 如果价格不可用，使用默认的固定值（向后兼容）
		currentPrice = 0
	}
	
	// 根据价格计算 amount：amount = 2000 / price（币数量）
	// 最大限制：2000 USDT，最小限制：确保 amount > 0
	const maxUSDTValue = 2000.0
	var currentAmount float64
	if currentPrice > 0 {
		currentAmount = maxUSDTValue / currentPrice
		// 如果价格太低导致 amount 过大，设置一个合理的上限（例如 1000 币）
		const maxAmount = 1000.0
		if currentAmount > maxAmount {
			currentAmount = maxAmount
		}
	} else {
		// 价格不可用时，使用默认值（向后兼容）
		currentAmount = t.slippageOpt.amount
	}
	
	// 计算 sourceA 的滑点（统一使用 CalculateSlippage，内部会判断是交易所还是链上）
	if t.sourceA != nil {
		isFutures := true
		if _, _, marketType, err := parseTraderTypeFromString(t.traderAType); err == nil && marketType == "spot" {
			isFutures = false
		}
		// CEX 需用带 USDT 的 symbol 拉订单簿；链上不 usesymbol，传任意即可
		symbolA := t.symbol
		if _, ok := t.sourceA.(trader.OnchainTrader); !ok {
			symbolA = normalizeSymbolForExchange(t.symbol)
		}
		buySlippage, _ := t.analytics.CalculateSlippage(t.sourceA, symbolA, currentAmount, isFutures, model.OrderSideBuy, slippageLimit)
		sellSlippage, _ := t.analytics.CalculateSlippage(t.sourceA, symbolA, currentAmount, isFutures, model.OrderSideSell, slippageLimit)
		if v, ok := t.analytics.(*analytics.Analytics); ok {
			v.SetSlippageForSource("A", model.OrderSideBuy, buySlippage)
			v.SetSlippageForSource("A", model.OrderSideSell, sellSlippage)
		}
	}

	// 计算 sourceB 的滑点
	if t.sourceB != nil {
		isFutures := true
		if _, _, marketType, err := parseTraderTypeFromString(t.traderBType); err == nil && marketType == "spot" {
			isFutures = false
		}
		symbolB := t.symbol
		if _, ok := t.sourceB.(trader.OnchainTrader); !ok {
			symbolB = normalizeSymbolForExchange(t.symbol)
		}
		buySlippage, _ := t.analytics.CalculateSlippage(t.sourceB, symbolB, currentAmount, isFutures, model.OrderSideBuy, slippageLimit)
		sellSlippage, _ := t.analytics.CalculateSlippage(t.sourceB, symbolB, currentAmount, isFutures, model.OrderSideSell, slippageLimit)
		if v, ok := t.analytics.(*analytics.Analytics); ok {
			v.SetSlippageForSource("B", model.OrderSideBuy, buySlippage)
			v.SetSlippageForSource("B", model.OrderSideSell, sellSlippage)
		}
	}

	// 记录滑点数据到 StatisticsManager
	t.recordSlippageToStatistics()
}

// SetExchangePrice 设置交易所价格数据
func (t *Trigger) SetExchangePrice(data *model.Ticker) {
	// 交易所为B
	t.directionAB.PriceData.BidPrice = data.BidPrice // B 的Bid
	t.directionBA.PriceData.AskPrice = data.AskPrice // B 的Ask
	t.lastTicker = data

	// 更新价格缓存到 PositionManager（用于定时更新 swapInfo.Amount）
	t.updatePriceCacheToPositionManager()

	// 记录价格数据到 StatisticsManager
	t.recordPriceToStatistics()
}

// SetOnChainPrice 设置链上价格数据
func (t *Trigger) SetOnChainPrice(data *model.ChainPriceInfo) {
	ask, _ := strconv.ParseFloat(data.ChainPriceSell, 64)
	bid, _ := strconv.ParseFloat(data.ChainPriceBuy, 64)

	// 链上为A
	t.directionAB.PriceData.AskPrice = ask // A 的Ask
	t.directionBA.PriceData.BidPrice = bid // A 的Bid

	// 更新价格缓存到 PositionManager（用于定时更新 swapInfo.Amount）
	t.updatePriceCacheToPositionManager()

	// 记录价格数据到 StatisticsManager
	t.recordPriceToStatistics()
}

// recordPriceToStatistics 记录价格数据到 StatisticsManager
func (t *Trigger) recordPriceToStatistics() {
	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager == nil {
		return
	}

	// 检查价格数据是否完整
	if t.directionAB.PriceData.BidPrice > 0 && t.directionAB.PriceData.AskPrice > 0 &&
		t.directionBA.PriceData.BidPrice > 0 && t.directionBA.PriceData.AskPrice > 0 {
		// 交易所价格：B 的 Bid 和 Ask
		exchangeBid := t.directionAB.PriceData.BidPrice // B 的 Bid
		exchangeAsk := t.directionBA.PriceData.AskPrice // B 的 Ask

		// 链上价格：A 的 Bid 和 Ask
		onchainBid := t.directionBA.PriceData.BidPrice // A 的 Bid
		onchainAsk := t.directionAB.PriceData.AskPrice // A 的 Ask

		priceData := &statistics.PriceData{
			ExchangeBid: exchangeBid,
			ExchangeAsk: exchangeAsk,
			OnchainBid:  onchainBid,
			OnchainAsk:  onchainAsk,
		}

		statisticsManager.RecordPrice(t.symbol, priceData)
	}
}

// recordSlippageToStatistics 记录滑点数据到 StatisticsManager
func (t *Trigger) recordSlippageToStatistics() {
	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager == nil {
		return
	}

	// 从 analytics 获取滑点数据
	slippageData := t.analytics.GetSlippageData()
	if slippageData == nil {
		return
	}

	// 构建滑点数据
	slippageDataForStats := &statistics.SlippageData{
		ExchangeBuy:  slippageData.ExchangeBuy,
		ExchangeSell: slippageData.ExchangeSell,
		OnChainBuy:   slippageData.OnChainBuy,
		OnChainSell:  slippageData.OnChainSell,
		ABuy:         slippageData.ABuy,
		ASell:        slippageData.ASell,
		BBuy:         slippageData.BBuy,
		BSell:        slippageData.BSell,
	}

	statisticsManager.RecordSlippage(t.symbol, slippageDataForStats)
}


