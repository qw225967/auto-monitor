package trigger

import (
	"fmt"
	"math"
	"strings"
	"time"

	"auto-arbitrage/internal/analytics"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/onchain"
	"auto-arbitrage/internal/position"
	"auto-arbitrage/internal/statistics"
	"auto-arbitrage/internal/statistics/monitor"
	"auto-arbitrage/internal/trader"
)

// extractExchangeType 从 traderType 中提取交易所类型（如 "binance:futures" -> "binance"）
func extractExchangeType(traderType string) string {
	if idx := strings.Index(traderType, ":"); idx > 0 {
		return traderType[:idx]
	}
	return traderType
}

// OrderCheckContext 订单检查上下文，封装检查所需的所有信息
type OrderCheckContext struct {
	direction     OrderDirection
	priceData     *model.PriceData // 当前方向的价格数据
	threshold     float64          // 当前方向的阈值
	diffValue     float64          // 当前方向的价差
	lastOrderTime *time.Time       // 上次下单时间
	latestTx      string           // 最新交易哈希
	enabled       bool             // 是否启用实际下单
}

// CheckResult 检查结果
type CheckResult struct {
	ShouldExecute bool
	SkipReason    string
	DiffValue     float64
	Threshold     float64
}

// checkAndExecuteOrderV2 统一的订单检查和执行方法（优化版本）
// 通过 OrderCheckContext 统一处理两个方向的逻辑，消除代码重复
func (t *Trigger) checkAndExecuteOrderV2(ctx *OrderCheckContext) *CheckResult {
	result := &CheckResult{
		DiffValue: ctx.diffValue,
		Threshold: ctx.threshold,
	}

	// 1. 价格数据验证（提前返回，避免不必要的计算）
	if !t.validatePriceData(ctx.priceData) {
		result.SkipReason = fmt.Sprintf("价格数据无效: Bid=%.6f, Ask=%.6f", ctx.priceData.BidPrice, ctx.priceData.AskPrice)
		return result
	}

	// 2. 启用状态检查（提前返回，避免后续检查）
	if !ctx.enabled {
		result.SkipReason = fmt.Sprintf("方向已禁用 (%s)", ctx.direction)
		t.logger.Debugf("跳过下单 %s: 方向已禁用", ctx.direction)
		return result
	}

	// 3. Trader 验证（提前返回，避免后续检查）
	if t.sourceA == nil || t.sourceB == nil {
		result.SkipReason = fmt.Sprintf("trader 不可用 (%s)", ctx.direction)
		return result
	}

	// 4. 交易哈希验证（仅在链上交易模式下检查）
	// 如果是纯交易所-交易所交易（没有 OnchainTrader），则跳过此检查
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
			return result
		}
	}

	// 5. 冷却时间检查
	if ctx.lastOrderTime != nil && time.Since(*ctx.lastOrderTime) < t.orderOpt.cooldown {
		remaining := t.orderOpt.cooldown - time.Since(*ctx.lastOrderTime)
		result.SkipReason = fmt.Sprintf("冷却时间未到，剩余: %v", remaining)
		return result
	}

	// 6. 价差检查
	// 对于正价差：价差必须大于阈值才触发（diffValue > threshold）
	// 对于负价差：价差必须小于等于阈值才触发（diffValue <= threshold，因为更负的价差绝对值更大）
	shouldTrigger := false
	if ctx.threshold >= 0 {
		// 正阈值：价差必须大于阈值
		shouldTrigger = ctx.diffValue > ctx.threshold
	} else {
		// 负阈值：价差必须小于等于阈值（更负的价差绝对值更大，应该触发）
		shouldTrigger = ctx.diffValue <= ctx.threshold
	}

	if !shouldTrigger {
		result.SkipReason = fmt.Sprintf("价差未达到阈值: %.6f%% (阈值: %.6f%%)", ctx.diffValue, ctx.threshold)
		return result
	}

	// 7. 仓位不均衡检查（可选）
	// 检查当前仓位是否严重不均衡，如果是，跳过会加剧不均衡的方向
	if skipReason := t.checkPositionImbalance(ctx.direction); skipReason != "" {
		result.SkipReason = skipReason
		return result
	}

	// 8. 价格趋势检查（仅针对 +A-B 方向，且 A 是链上、价差为负时）
	// 在阈值检查通过后，最后检查价格趋势
	if ctx.direction == DirectionAB {
		// 检查 A 是否是链上 trader
		isAOnchain := false
		if t.sourceA != nil {
			if _, ok := t.sourceA.(trader.OnchainTrader); ok {
				isAOnchain = true
			}
		}

		// 只有当 A 是链上且价差为负时，才需要检查价格趋势
		if isAOnchain && ctx.diffValue < 0 {
			// 价格趋势检查：价格上涨时才允许 +A-B 开仓
			if !t.checkPriceTrend() {
				// 记录被过滤的订单到监控系统
				monitorInstance := monitor.GetExecutionMonitor()
				// 获取计划执行数量（使用默认值，因为过滤时可能没有完整的价格数据）
				plannedSize := DefaultOrderSize
				// 尝试从 position 获取 size（如果可能的话）
				if positionManager := position.GetPositionManager(); positionManager != nil {
					// 准备基本参数
					exchangeType := ""
					chainIndex := t.onChainData.ChainIndex
					if chainIndex == "" {
						chainIndex = "56" // 默认 BSC
					}
					// 使用当前价格数据
					exchangePriceData := ctx.priceData
					onchainPriceData := &t.directionBA.PriceData
					traderAType := t.traderAType
					traderBType := t.traderBType

					// 尝试获取 size（如果价格数据有效）
					if exchangePriceData.BidPrice > 0 && exchangePriceData.AskPrice > 0 {
						size := positionManager.GetSize(t.symbol, "AB", exchangeType, chainIndex, exchangePriceData, onchainPriceData, traderAType, traderBType)
						if size > 0 {
							plannedSize = size
						}
					}
				}
				monitorInstance.RecordFilteredExecution(
					t.symbol,
					"AB",
					ctx.diffValue,
					ctx.threshold,
					plannedSize,
					"价格趋势检查未通过：价格下降，不允许 +A-B 开仓（A是链上且价差为负）",
				)
				result.SkipReason = "价格趋势检查未通过：价格下降，不允许 +A-B 开仓（A是链上且价差为负）"
				return result
			}
		}
	}

	// 9. 所有检查通过，执行订单
	result.ShouldExecute = true
	t.logger.Infof("触发下单 %s: 价差=%.6f%%, 阈值=%.6f%%", ctx.direction, ctx.diffValue, ctx.threshold)

	// 执行订单
	t.executeOrderV2(ctx)

	// 更新最后下单时间
	now := time.Now()
	if ctx.lastOrderTime != nil {
		*ctx.lastOrderTime = now
	}

	return result
}

// validatePriceData 验证价格数据有效性
// 检查 BidPrice 和 AskPrice 是否都大于 0
func (t *Trigger) validatePriceData(priceData *model.PriceData) bool {
	if priceData == nil {
		return false
	}
	return priceData.BidPrice > 0 && priceData.AskPrice > 0
}

// orderExecutionContext 订单执行上下文，包含执行过程中需要的所有数据
type orderExecutionContext struct {
	directionStr      string
	exchangePriceData *model.PriceData
	onchainPriceData  *model.PriceData
	exchangeType      string
	orderSize         float64
	monitorInstance   *monitor.ExecutionMonitor
	record            *monitor.ExecutionRecord
	traderA           trader.Trader            // sourceA trader
	traderB           trader.Trader            // sourceB trader
	orderA            *model.PlaceOrderRequest // A 的订单请求
	orderB            *model.PlaceOrderRequest // B 的订单请求
	needsSequential   bool                     // 是否需要顺序执行（有链上则先链上后交易所）
	sequentialFirstA  bool                     // 顺序执行时是否先执行 A（否则先 B）；仅 needsSequential 时有效
}

// executeOrderV2 执行订单（统一使用 trader 接口，支持顺序和并发执行）
func (t *Trigger) executeOrderV2(ctx *OrderCheckContext) {
	t.logger.Infof(">>> 发起订单 %s | 价差: %.6f%%", ctx.direction, ctx.diffValue)

	// 1. 准备执行上下文
	execCtx, shouldReturn := t.prepareOrderContext(ctx)
	if shouldReturn {
		return
	}

	// 2. 初始化监控并设置 panic 恢复
	execCtx.monitorInstance = monitor.GetExecutionMonitor()
	// 判断 A 和 B 是否是链上 trader
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
		// 顺序执行：先 A 后 B（A 的结果可能影响 B 的数量）
		orderA, orderB, onchainResultA, onchainResultB, err = t.executeTradersSequentially(ctx, execCtx)
	} else {
		// 并发执行：A 和 B 同时执行
		orderA, orderB, onchainResultA, onchainResultB, err = t.executeTradersConcurrently(ctx, execCtx)
	}

	if err != nil {
		return // 错误已在执行函数中处理
	}

	// 4. 计算实际大小并记录统计
	actualSize, _, _ := t.calculateActualSizes(orderA, orderB, execCtx.orderSize)
	t.logOrderCompletion(ctx, execCtx, orderA, orderB, actualSize, execCtx.orderSize)

	// 判断 A 和 B 的类型（复用之前的值）
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

	// 异步记录：开协程分别查询 A/B 成交状态，获取完整后再记录
	go t.recordTradeToStatisticsAsync(ctx, execCtx, actualSize, orderA, orderB, onchainResultA, onchainResultB, isAOnchain, isBOnchain)

	// 5. 交易成功后，刷新余额并更新 swapInfo.Amount
	// 这确保后续交易使用最新的余额数据，避免余额不足错误
	if isAOnchain || isBOnchain {
		positionManager := position.GetPositionManager()
		if positionManager != nil {
			// 异步刷新，不阻塞当前流程
			go func() {
				t.logger.Debugf("交易成功，触发余额刷新和 swapInfo.Amount 更新")
				positionManager.ForceRefreshAndUpdate(t.symbol)
			}()
		}
	}
}

// prepareOrderContext 准备订单执行上下文
func (t *Trigger) prepareOrderContext(ctx *OrderCheckContext) (*orderExecutionContext, bool) {
	execCtx := &orderExecutionContext{
		directionStr:      t.getDirectionString(ctx.direction),
		exchangePriceData: ctx.priceData,
	}

	// 获取链上价格数据（如果有链上 trader）
	onchainTrader := t.getOnchainTrader()
	execCtx.onchainPriceData = t.getOnchainPriceData(execCtx.exchangePriceData, onchainTrader)

	// 获取交易所类型
	execCtx.exchangeType = t.getExchangeType()

	// 计算订单大小
	orderSize, ok := t.calculateOrderSize(execCtx.directionStr, execCtx.exchangeType, execCtx.exchangePriceData, execCtx.onchainPriceData)
	if !ok {
		return nil, true
	}
	execCtx.orderSize = orderSize

	// 确定 trader A 和 trader B（根据方向）
	execCtx.traderA, execCtx.traderB = t.determineTradersForDirection(ctx.direction)

	// 构建订单请求
	execCtx.orderA, execCtx.orderB = t.buildOrderRequests(ctx.direction, orderSize)

	// 判断是否需要顺序执行
	// 如果 A 是链上 trader 且 B 需要根据 A 的结果调整数量，则需要顺序执行
	execCtx.needsSequential = t.shouldExecuteSequentially(execCtx.traderA, execCtx.traderB)
	execCtx.sequentialFirstA = execCtx.needsSequential // 通用 trigger 保持先 A 后 B

	return execCtx, false
}

// getDirectionString 获取方向字符串
func (t *Trigger) getDirectionString(direction OrderDirection) string {
	if direction == DirectionAB {
		return "AB"
	}
	return "BA"
}

// getOnchainPriceData 获取链上价格数据
func (t *Trigger) getOnchainPriceData(exchangePriceData *model.PriceData, chainTrader trader.OnchainTrader) *model.PriceData {
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
func (t *Trigger) getExchangeType() string {
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

// calculateOrderSize 计算订单大小
func (t *Trigger) calculateOrderSize(directionStr, exchangeType string, exchangePriceData, onchainPriceData *model.PriceData) (float64, bool) {
	positionManager := position.GetPositionManager()
	if positionManager == nil {
		t.logger.Warn("PositionManager 未初始化，使用默认 size")
		return DefaultOrderSize, true
	}

	orderSize := positionManager.GetSize(t.symbol, directionStr, exchangeType, t.onChainData.ChainIndex, exchangePriceData, onchainPriceData, t.traderAType, t.traderBType)
	if orderSize <= 0 {
		t.logger.Warnf("PositionManager 返回的 size 为 %.6f，使用默认 size", orderSize)
		return 0, false
	}

	// 订单 USDT 价值限制：不低于 200 USDT，不高于 2000 USDT
	const minUSDTValue = 200.0
	const maxUSDTValue = 2000.0
	var price float64
	if directionStr == "AB" {
		// AB 方向：链上买入，交易所卖出，使用交易所卖出价格（BidPrice）
		if exchangePriceData != nil && exchangePriceData.BidPrice > 0 {
			price = exchangePriceData.BidPrice
		} else {
			t.logger.Warnf("无法获取 AB 方向价格，跳过 USDT 限制")
			return orderSize, true
		}
	} else {
		// BA 方向：链上卖出，交易所买入，使用交易所买入价格（AskPrice）
		if exchangePriceData != nil && exchangePriceData.AskPrice > 0 {
			price = exchangePriceData.AskPrice
		} else {
			t.logger.Warnf("无法获取 BA 方向价格，跳过 USDT 限制")
			return orderSize, true
		}
	}

	// 计算当前订单的 USDT 价值
	orderValueUSDT := orderSize * price
	if orderValueUSDT < minUSDTValue {
		// 订单价值低于最小限制，跳过此次交易
		t.logger.Infof("订单 USDT 价值 %.2f 低于最小限制 %.2f，跳过此次交易（size: %.6f）",
			orderValueUSDT, minUSDTValue, orderSize)
		return 0, false
	}
	if orderValueUSDT > maxUSDTValue {
		// 订单价值超过最大限制，限制为 2000 USDT
		limitedSize := maxUSDTValue / price
		t.logger.Infof("订单 USDT 价值 %.2f 超过最大限制 %.2f，将 size 从 %.6f 限制为 %.6f",
			orderValueUSDT, maxUSDTValue, orderSize, limitedSize)
		return limitedSize, true
	}

	return orderSize, true
}

// determineTradersForDirection 根据方向确定 trader A 和 trader B
func (t *Trigger) determineTradersForDirection(direction OrderDirection) (trader.Trader, trader.Trader) {
	// A 始终是 sourceA，B 始终是 sourceB
	return t.sourceA, t.sourceB
}

// shouldExecuteSequentially 判断是否需要顺序执行（先A后B）
// 如果 A 是链上 trader，需要顺序执行以获取实际成交数量来调整 B
func (t *Trigger) shouldExecuteSequentially(traderA, traderB trader.Trader) bool {
	// 如果 A 是链上 trader，需要顺序执行
	if _, ok := traderA.(trader.OnchainTrader); ok {
		return true
	}
	// 其他情况可以并发执行
	return false
}

// buildOrderRequests 根据方向构建 A 和 B 的订单请求
func (t *Trigger) buildOrderRequests(direction OrderDirection, orderSize float64) (*model.PlaceOrderRequest, *model.PlaceOrderRequest) {
	if direction == DirectionAB {
		// +A-B: A 买入，B 卖出
		return &model.PlaceOrderRequest{
				Symbol:     t.symbol,
				Side:       model.OrderSideBuy,
				Type:       model.OrderTypeMarket,
				Quantity:   orderSize,
				MarketType: model.MarketTypeFutures,
			}, &model.PlaceOrderRequest{
				Symbol:     t.symbol,
				Side:       model.OrderSideSell,
				Type:       model.OrderTypeMarket,
				Quantity:   orderSize,
				MarketType: model.MarketTypeFutures,
			}
	}
	// -A+B: A 卖出，B 买入
	return &model.PlaceOrderRequest{
			Symbol:     t.symbol,
			Side:       model.OrderSideSell,
			Type:       model.OrderTypeMarket,
			Quantity:   orderSize,
			MarketType: model.MarketTypeFutures,
		}, &model.PlaceOrderRequest{
			Symbol:     t.symbol,
			Side:       model.OrderSideBuy,
			Type:       model.OrderTypeMarket,
			Quantity:   orderSize,
			MarketType: model.MarketTypeFutures,
		}
}

// handleOrderPanic 处理订单执行过程中的 panic
func (t *Trigger) handleOrderPanic(execCtx *orderExecutionContext) {
	if r := recover(); r != nil {
		errMsg := fmt.Sprintf("Panic: %v", r)
		execCtx.monitorInstance.FailExecution(execCtx.record, errMsg)
		t.logger.Errorf("执行订单时发生 Panic: %v", r)
	}
}

// executeTradersSequentially 顺序执行交易（先 A 后 B）
// 返回: (orderA, orderB, onchainResultA, onchainResultB, error)
func (t *Trigger) executeTradersSequentially(ctx *OrderCheckContext, execCtx *orderExecutionContext) (*model.Order, *model.Order, *trader.OnchainTradeResult, *trader.OnchainTradeResult, error) {
	t.logger.Infof(">>> 订单执行 %s | 模式: 顺序执行（先A后B）", ctx.direction)

	// 1. 先执行 A
	orderA, onchainResultA, err := t.executeTraderOrder(execCtx.traderA, execCtx.orderA, ctx.direction, true, execCtx.record)
	if err != nil {
		t.logger.Errorf(">>> 订单失败(A) %s | 错误: %v", ctx.direction, err)
		return nil, nil, nil, nil, err
	}

	// 2. 如果 A 是链上交易且有结果，根据实际成交数量调整 B 的订单数量
	if onchainResultA != nil && onchainResultA.CoinAmount > 0 {
		originalQuantity := execCtx.orderB.Quantity
		execCtx.orderB.Quantity = math.Ceil(onchainResultA.CoinAmount)
		t.logger.Infof("  [订单调整] 使用 A 实际成交数量: %.6f -> %.0f (原计划: %.6f)", onchainResultA.CoinAmount, execCtx.orderB.Quantity, originalQuantity)
	}

	// 3. 执行 B
	orderB, onchainResultB, err := t.executeTraderOrder(execCtx.traderB, execCtx.orderB, ctx.direction, false, execCtx.record)
	if err != nil {
		t.logger.Errorf(">>> 订单失败(B) %s | 错误: %v", ctx.direction, err)
		return orderA, nil, onchainResultA, nil, err
	}

	return orderA, orderB, onchainResultA, onchainResultB, nil
}

// executeTradersConcurrently 并发执行交易（A 和 B 同时执行）
// 返回: (orderA, orderB, onchainResultA, onchainResultB, error)
func (t *Trigger) executeTradersConcurrently(ctx *OrderCheckContext, execCtx *orderExecutionContext) (*model.Order, *model.Order, *trader.OnchainTradeResult, *trader.OnchainTradeResult, error) {
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

	// 处理错误
	if errA != nil {
		t.logger.Errorf(">>> 订单失败(A) %s | 错误: %v", ctx.direction, errA)
		return nil, nil, nil, nil, errA
	}
	if errB != nil {
		t.logger.Errorf(">>> 订单失败(B) %s | 错误: %v", ctx.direction, errB)
		return orderA, nil, onchainResultA, nil, errB
	}

	return orderA, orderB, onchainResultA, onchainResultB, nil
}

// executeTraderOrder 统一执行 trader 订单（支持链上和交易所）
func (t *Trigger) executeTraderOrder(tr trader.Trader, req *model.PlaceOrderRequest, direction OrderDirection, isA bool, record *monitor.ExecutionRecord) (*model.Order, *trader.OnchainTradeResult, error) {
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
			// 价格从请求中获取或使用默认值
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
func (t *Trigger) calculateActualSizes(orderA, orderB *model.Order, plannedSize float64) (float64, float64, float64) {
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
func (t *Trigger) logOrderCompletion(ctx *OrderCheckContext, execCtx *orderExecutionContext, orderA, orderB *model.Order, actualSize, orderSize float64) {
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

// recordTradeToStatisticsAsync 成交完成后开协程分别查询 A/B 成交状态，获取完整后再记录
func (t *Trigger) recordTradeToStatisticsAsync(ctx *OrderCheckContext, execCtx *orderExecutionContext,
	orderSize float64, orderA, orderB *model.Order, onchainResultA, onchainResultB *trader.OnchainTradeResult,
	isAOnchain, isBOnchain bool) {
	orderAUpdated := orderA
	orderBUpdated := orderB

	// 分别查询 CEX 端的成交详情（链上数据已完整，无需重查）
	if !isAOnchain && orderA != nil && orderA.OrderID != "" && execCtx.orderA != nil {
		if o := t.pollCexOrderUntilFilled(execCtx.traderA, execCtx.orderA.Symbol, orderA.OrderID, execCtx.orderA.MarketType); o != nil {
			orderAUpdated = o
		}
	}
	if !isBOnchain && orderB != nil && orderB.OrderID != "" && execCtx.orderB != nil {
		if o := t.pollCexOrderUntilFilled(execCtx.traderB, execCtx.orderB.Symbol, orderB.OrderID, execCtx.orderB.MarketType); o != nil {
			orderBUpdated = o
		}
	}

	t.recordTradeToStatistics(ctx, execCtx.directionStr, orderSize, orderAUpdated, orderBUpdated, onchainResultA, onchainResultB, isAOnchain, isBOnchain, execCtx.exchangePriceData)
}

// pollCexOrderUntilFilled 轮询 CEX 订单直到成交或超时，返回完整成交信息的 Order
func (t *Trigger) pollCexOrderUntilFilled(tr trader.Trader, symbol, orderID string, marketType model.MarketType) *model.Order {
	cex, ok := tr.(*trader.CexTrader)
	if !ok {
		return nil
	}
	const maxRetries = 10
	const intervalMs = 500
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			time.Sleep(intervalMs * time.Millisecond)
		}
		o, err := cex.QueryOrderDetails(symbol, orderID, marketType)
		if err != nil {
			t.logger.Warnf("  [异步记录] 查询 A/B 成交失败(第%d次): %v", i+1, err)
			continue
		}
		if o != nil && o.FilledQty > 0 && o.FilledPrice > 0 {
			t.logger.Infof("  [异步记录] 获取完整成交 | OrderID=%s FilledQty=%.6f FilledPrice=%.6f Fee=%.6f", orderID, o.FilledQty, o.FilledPrice, o.Fee)
			return o
		}
	}
	t.logger.Warnf("  [异步记录] 轮询 %d 次后仍未获取完整成交，使用已有数据记录", maxRetries)
	return nil
}

// recordTradeToStatistics 记录成交数据到 StatisticsManager（使用实际交易数据）
// 根据 A 和 B 的实际类型计算收益，而不是固定的链上-交易所模式
func (t *Trigger) recordTradeToStatistics(ctx *OrderCheckContext, directionStr string, orderSize float64,
	orderA, orderB *model.Order, onchainResultA, onchainResultB *trader.OnchainTradeResult,
	isAOnchain, isBOnchain bool, exchangePriceData *model.PriceData) {
	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager == nil {
		return
	}

	// 获取链上价格数据（用于成本计算）
	onchainPriceData := &model.PriceData{
		BidPrice: t.directionBA.PriceData.BidPrice, // 链上 Bid
		AskPrice: t.directionAB.PriceData.AskPrice, // 链上 Ask
	}

	// 获取滑点数据
	slippageData := t.analytics.GetSlippageData()
	if slippageData == nil {
		slippageData = &analytics.SlippageData{}
	}

	// 计算成本
	costData := t.analytics.CalculateCostForDirection(directionStr, orderSize, exchangePriceData, onchainPriceData)
	if costData == nil {
		costData = &analytics.CostData{}
	}

	// 根据方向确定 A 和 B 的操作
	// directionStr: "AB" 表示 +A-B（A 买入，B 卖出）
	// directionStr: "BA" 表示 -A+B（A 卖出，B 买入）
	var sideA, sideB model.OrderSide
	if directionStr == "AB" {
		sideA = model.OrderSideBuy  // A 买入
		sideB = model.OrderSideSell // B 卖出
	} else {
		sideA = model.OrderSideSell // A 卖出
		sideB = model.OrderSideBuy  // B 买入
	}

	// 计算 A 的成本/收入
	var costA, revenueA, feeA, gasFeeA float64
	if isAOnchain {
		// A 是链上交易
		if sideA == model.OrderSideBuy {
			// A 买入：消耗 USDT
			if onchainResultA != nil {
				costA = onchainResultA.AmountInFloat // USDT -> Coin，AmountIn 是 USDT
				gasFeeA = onchainResultA.GasFee
				// 🔥 兜底：如果 AmountInFloat 为 0，使用 CoinAmount 和价格估算
				if costA <= 0 && onchainResultA.CoinAmount > 0 && exchangePriceData != nil && exchangePriceData.AskPrice > 0 {
					costA = onchainResultA.CoinAmount * exchangePriceData.AskPrice
					t.logger.Debugf("A买入兜底估算: costA=%.2f (CoinAmount=%.2f * AskPrice=%.6f)", costA, onchainResultA.CoinAmount, exchangePriceData.AskPrice)
				}
			} else if orderA != nil && orderA.FilledQty > 0 && exchangePriceData != nil && exchangePriceData.AskPrice > 0 {
				// 兜底：使用订单数量和价格估算
				costA = orderA.FilledQty * exchangePriceData.AskPrice
			}
		} else {
			// A 卖出：获得 USDT
			if onchainResultA != nil {
				revenueA = onchainResultA.AmountOutFloat // Coin -> USDT，AmountOut 是 USDT
				gasFeeA = onchainResultA.GasFee
				// 🔥 兜底：如果 AmountOutFloat 为 0，使用 CoinAmount 和价格估算
				if revenueA <= 0 && onchainResultA.CoinAmount > 0 && exchangePriceData != nil && exchangePriceData.BidPrice > 0 {
					revenueA = onchainResultA.CoinAmount * exchangePriceData.BidPrice
					t.logger.Debugf("A卖出兜底估算: revenueA=%.2f (CoinAmount=%.2f * BidPrice=%.6f)", revenueA, onchainResultA.CoinAmount, exchangePriceData.BidPrice)
				}
			} else if orderA != nil && orderA.FilledQty > 0 && exchangePriceData != nil && exchangePriceData.BidPrice > 0 {
				// 兜底：使用订单数量和价格估算
				revenueA = orderA.FilledQty * exchangePriceData.BidPrice
			}
		}
	} else {
		// A 是交易所交易
		if orderA != nil && orderA.FilledQty > 0 && orderA.FilledPrice > 0 {
			if sideA == model.OrderSideBuy {
				// A 买入：消耗 USDT
				costA = orderA.FilledQty * orderA.FilledPrice
				feeA = orderA.Fee
			} else {
				// A 卖出：获得 USDT
				revenueA = orderA.FilledQty * orderA.FilledPrice
				feeA = orderA.Fee
			}
		} else if exchangePriceData != nil {
			// 兜底：使用价格数据估算
			if sideA == model.OrderSideBuy {
				costA = orderSize * exchangePriceData.AskPrice
				feeA = costA * (analytics.ExchangeFeeRate / 100.0)
			} else {
				revenueA = orderSize * exchangePriceData.BidPrice
				feeA = revenueA * (analytics.ExchangeFeeRate / 100.0)
			}
		}
	}

	// 计算 B 的成本/收入
	var costB, revenueB, feeB, gasFeeB float64
	if isBOnchain {
		// B 是链上交易
		if sideB == model.OrderSideBuy {
			// B 买入：消耗 USDT
			if onchainResultB != nil {
				costB = onchainResultB.AmountInFloat
				gasFeeB = onchainResultB.GasFee
				// 🔥 兜底：如果 AmountInFloat 为 0，使用 CoinAmount 和价格估算
				if costB <= 0 && onchainResultB.CoinAmount > 0 && exchangePriceData != nil && exchangePriceData.AskPrice > 0 {
					costB = onchainResultB.CoinAmount * exchangePriceData.AskPrice
					t.logger.Debugf("B买入兜底估算: costB=%.2f (CoinAmount=%.2f * AskPrice=%.6f)", costB, onchainResultB.CoinAmount, exchangePriceData.AskPrice)
				}
			} else if orderB != nil && orderB.FilledQty > 0 && exchangePriceData != nil && exchangePriceData.AskPrice > 0 {
				costB = orderB.FilledQty * exchangePriceData.AskPrice
			}
		} else {
			// B 卖出：获得 USDT
			if onchainResultB != nil {
				revenueB = onchainResultB.AmountOutFloat
				gasFeeB = onchainResultB.GasFee
				// 🔥 兜底：如果 AmountOutFloat 为 0，使用 CoinAmount 和价格估算
				if revenueB <= 0 && onchainResultB.CoinAmount > 0 && exchangePriceData != nil && exchangePriceData.BidPrice > 0 {
					revenueB = onchainResultB.CoinAmount * exchangePriceData.BidPrice
					t.logger.Debugf("B卖出兜底估算: revenueB=%.2f (CoinAmount=%.2f * BidPrice=%.6f)", revenueB, onchainResultB.CoinAmount, exchangePriceData.BidPrice)
				}
			} else if orderB != nil && orderB.FilledQty > 0 && exchangePriceData != nil && exchangePriceData.BidPrice > 0 {
				revenueB = orderB.FilledQty * exchangePriceData.BidPrice
			}
		}
	} else {
		// B 是交易所交易
		if orderB != nil && orderB.FilledQty > 0 && orderB.FilledPrice > 0 {
			if sideB == model.OrderSideBuy {
				// B 买入：消耗 USDT
				costB = orderB.FilledQty * orderB.FilledPrice
				feeB = orderB.Fee
			} else {
				// B 卖出：获得 USDT
				revenueB = orderB.FilledQty * orderB.FilledPrice
				feeB = orderB.Fee
			}
		} else if exchangePriceData != nil {
			// 兜底：使用价格数据估算
			if sideB == model.OrderSideBuy {
				costB = orderSize * exchangePriceData.AskPrice
				feeB = costB * (analytics.ExchangeFeeRate / 100.0)
			} else {
				revenueB = orderSize * exchangePriceData.BidPrice
				feeB = revenueB * (analytics.ExchangeFeeRate / 100.0)
			}
		}
	}

	// 计算收益：总收入 - 总成本 - 总手续费 - 总 Gas 费
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

	t.logger.Infof("💰 收益(%s) | A收入=%.2f, A成本=%.2f, A手续费=%.2f, A Gas=%.2f | B收入=%.2f, B成本=%.2f, B手续费=%.2f, B Gas=%.2f | 总收益=%.2f USDT",
		directionStr, revenueA, costA, feeA, gasFeeA, revenueB, costB, feeB, gasFeeB, profit)

	// 计算币本位成本
	var costInCoin float64
	var costPercent float64
	if price > 0 && orderSize > 0 {
		costInCoin = costData.TotalCost / price
		costPercent = (costInCoin / orderSize) * 100.0
	}

	// 记录成本数据到 StatisticsManager（设置 tempCostData）
	costDataForStats := &statistics.CostData{
		CostInCoin:    costInCoin,
		CostPercent:   costPercent,
		TotalCostUSDT: costData.TotalCost,
	}
	statisticsManager.RecordCost(t.symbol, costDataForStats)

	// 记录Size数据到 StatisticsManager（设置 tempSizeData）
	sizeDataForStats := &statistics.SizeData{
		Size:     orderSize,
		SizeUSDT: sizeUSDT,
	}
	statisticsManager.RecordSize(t.symbol, sizeDataForStats)

	// 构建成交记录
	tradeRecord := &statistics.TradeRecord{
		Direction:   directionStr,
		Size:        orderSize,
		SizeUSDT:    sizeUSDT,
		Price:       price,
		DiffValue:   ctx.diffValue,
		Profit:      profit,
		CostInCoin:  costInCoin,
		CostPercent: costPercent,
	}

	statisticsManager.RecordTrade(t.symbol, tradeRecord)

	// 更新 Analytics 的最后成交时间（用于成交停滞检测）
	if v, ok := t.analytics.(*analytics.Analytics); ok {
		v.UpdateLastTradeTime()
		t.logger.Infof("🔔 已更新 lastTradeTime: symbol=%s, direction=%s, size=%.6f, newTime=%v",
			t.symbol, directionStr, orderSize, time.Now().Format("15:04:05.000"))
	} else {
		t.logger.Warnf("无法更新最后成交时间: Analytics 类型断言失败")
	}
}

// checkPriceTrend 检查价格趋势（归一化后计算斜率）
// 返回 true 表示价格上涨（斜率为正），false 表示价格下降（斜率为负）
// 如果数据不足或无法判断，返回 true（允许开仓，保守策略）
func (t *Trigger) checkPriceTrend() bool {
	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager == nil {
		t.logger.Debugf("StatisticsManager 未初始化，跳过价格趋势检查")
		return true // 数据不足时允许开仓
	}

	// 获取最近1分钟的价格记录（约120个数据点，每500ms记录一次）
	priceRecords := statisticsManager.GetPriceRecords(t.symbol)
	if priceRecords == nil {
		t.logger.Debugf("未找到 %s 的统计数据，跳过价格趋势检查", t.symbol)
		return true
	}

	// 需要至少30个数据点（约15秒）才能判断趋势
	const minDataPoints = 30
	if len(priceRecords) < minDataPoints {
		t.logger.Debugf("价格数据点不足（%d < %d），跳过价格趋势检查", len(priceRecords), minDataPoints)
		return true
	}

	// 取最近1分钟的数据（约120个点）
	recentRecords := priceRecords
	if len(recentRecords) > 120 {
		recentRecords = recentRecords[len(recentRecords)-120:]
	}

	// 使用交易所中间价（(Bid + Ask) / 2）作为价格基准
	// 归一化：将价格转换为相对于第一个价格的百分比变化
	normalizedPrices := make([]float64, 0, len(recentRecords))
	basePrice := 0.0

	for i, record := range recentRecords {
		// 使用交易所中间价
		midPrice := (record.ExchangeBid + record.ExchangeAsk) / 2.0
		if midPrice <= 0 {
			continue
		}

		if i == 0 {
			basePrice = midPrice
		}

		// 归一化：计算相对于基准价格的百分比变化
		normalizedPrice := (midPrice - basePrice) / basePrice * 100.0
		normalizedPrices = append(normalizedPrices, normalizedPrice)
	}

	if len(normalizedPrices) < minDataPoints {
		t.logger.Debugf("归一化后有效数据点不足（%d < %d），跳过价格趋势检查", len(normalizedPrices), minDataPoints)
		return true
	}

	// 使用线性回归计算斜率
	// 斜率 = Σ((x - x̄)(y - ȳ)) / Σ((x - x̄)²)
	n := float64(len(normalizedPrices))
	sumX := 0.0
	sumY := 0.0
	sumXY := 0.0
	sumX2 := 0.0

	for i, y := range normalizedPrices {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	meanX := sumX / n
	meanY := sumY / n

	numerator := sumXY - n*meanX*meanY
	denominator := sumX2 - n*meanX*meanX

	if denominator == 0 {
		t.logger.Debugf("斜率计算分母为0，跳过价格趋势检查")
		return true
	}

	slope := numerator / denominator

	// 斜率 > 0 表示价格上涨，允许 +A-B 开仓
	// 斜率 <= 0 表示价格下降或持平，不允许 +A-B 开仓
	if slope > 0 {
		t.logger.Debugf("价格趋势检查通过：斜率=%.6f（价格上涨），允许 +A-B 开仓", slope)
		return true
	} else {
		t.logger.Debugf("价格趋势检查未通过：斜率=%.6f（价格下降），不允许 +A-B 开仓", slope)
		return false
	}
}

// checkAndExecuteOrderABV2 +A-B 方向的订单检查和执行（优化版本）
func (t *Trigger) checkAndExecuteOrderABV2() *CheckResult {
	t.executingMu.Lock()
	if t.executingAB {
		t.executingMu.Unlock()
		return &CheckResult{} // 已有 +A-B 正在执行，跳过，防止链上/交易所不均衡
	}
	t.executingAB = true
	t.executingMu.Unlock()
	defer func() {
		t.executingMu.Lock()
		t.executingAB = false
		t.executingMu.Unlock()
	}()

	// 先获取阈值和计算价差，以便在过滤时也能记录这些信息
	thresholdAB, _, err := t.analytics.GetThreshold()
	if err != nil {
		t.logger.Debugf("Failed to get thresholds: %v, skipping +A-B order check", err)
		return &CheckResult{SkipReason: fmt.Sprintf("获取阈值失败: %v", err)}
	}

	// 计算价差
	diff, err := CalculateDiff(&t.directionAB.PriceData, &t.directionBA.PriceData)
	if err != nil {
		t.logger.Errorf("CalculateDiff err:%v", err)
		return &CheckResult{SkipReason: fmt.Sprintf("计算价差失败: %v", err)}
	}

	// 构建检查上下文
	ctx := &OrderCheckContext{
		direction:     DirectionAB,
		priceData:     &t.directionAB.PriceData,
		threshold:     thresholdAB,
		diffValue:     diff.DiffAB,
		lastOrderTime: &t.directionAB.LastOrderTime,
		latestTx:      t.onChainData.BuyTx,
		enabled:       t.directionAB.OrderExecutionEnabled,
	}

	return t.checkAndExecuteOrderV2(ctx)
}

// checkAndExecuteOrderBAV2 -A+B 方向的订单检查和执行（优化版本）
func (t *Trigger) checkAndExecuteOrderBAV2() *CheckResult {
	t.executingMu.Lock()
	if t.executingBA {
		t.executingMu.Unlock()
		return &CheckResult{} // 已有 -A+B 正在执行，跳过，防止链上/交易所不均衡
	}
	t.executingBA = true
	t.executingMu.Unlock()
	defer func() {
		t.executingMu.Lock()
		t.executingBA = false
		t.executingMu.Unlock()
	}()

	// 获取阈值
	_, thresholdBA, err := t.analytics.GetThreshold()
	if err != nil {
		t.logger.Debugf("Failed to get thresholds: %v, skipping -A+B order check", err)
		return &CheckResult{SkipReason: fmt.Sprintf("获取阈值失败: %v", err)}
	}

	// 计算价差
	diff, err := CalculateDiff(&t.directionAB.PriceData, &t.directionBA.PriceData)
	if err != nil {
		t.logger.Errorf("CalculateDiff err:%v", err)
		return &CheckResult{SkipReason: fmt.Sprintf("计算价差失败: %v", err)}
	}

	// 构建检查上下文
	ctx := &OrderCheckContext{
		direction:     DirectionBA,
		priceData:     &t.directionBA.PriceData,
		threshold:     thresholdBA,
		diffValue:     diff.DiffBA,
		lastOrderTime: &t.directionBA.LastOrderTime,
		latestTx:      t.onChainData.SellTx,
		enabled:       t.directionBA.OrderExecutionEnabled,
	}

	return t.checkAndExecuteOrderV2(ctx)
}

// checkPositionImbalance 检查仓位不均衡
// 如果当前仓位严重不均衡，且该方向会加剧不均衡，则返回跳过原因
// direction: 当前要执行的方向
// 返回: 空字符串表示可以执行，非空表示跳过原因
func (t *Trigger) checkPositionImbalance(direction OrderDirection) string {
	positionManager := position.GetPositionManager()
	if positionManager == nil {
		return "" // 没有 PositionManager，不做检查
	}

	// 获取当前 symbol 的仓位汇总
	summary := positionManager.GetSymbolPositionSummary(t.symbol)
	if summary == nil {
		return "" // 没有仓位数据，不做检查
	}

	// 计算净仓位
	// TotalQuantity = 交易所多头 - 交易所空头 + 链上余额
	netPosition := summary.TotalQuantity

	// 定义不均衡阈值（可配置）
	// 如果净仓位超过一定数量，认为不均衡
	// 这里使用链上余额或交易所仓位的一定比例作为阈值
	imbalanceThreshold := float64(0)
	if summary.TotalOnchainBalance > 0 {
		imbalanceThreshold = summary.TotalOnchainBalance * 0.5 // 50% 的链上余额
	} else if summary.TotalExchangeLongSize > 0 || summary.TotalExchangeShortSize > 0 {
		maxSize := math.Max(summary.TotalExchangeLongSize, summary.TotalExchangeShortSize)
		imbalanceThreshold = maxSize * 0.5 // 50% 的最大仓位
	}

	// 如果阈值为 0，不做检查
	if imbalanceThreshold <= 0 {
		return ""
	}

	// 检查不均衡情况
	// AB (+A-B): 链上买入 + 交易所卖出 → 增加链上持仓，减少交易所空头
	// BA (-A+B): 链上卖出 + 交易所买入 → 减少链上持仓，减少交易所多头
	if direction == DirectionAB {
		// AB 方向会增加净仓位（链上买入）
		if netPosition > imbalanceThreshold {
			t.logger.Warnf("⚠️ 仓位不均衡检测: 净仓位=%.2f > 阈值=%.2f，跳过 AB 方向（会增加不均衡）",
				netPosition, imbalanceThreshold)
			return fmt.Sprintf("仓位不均衡: 净仓位=%.2f, 跳过 AB 方向", netPosition)
		}
	} else {
		// BA 方向会减少净仓位（链上卖出）
		if netPosition < -imbalanceThreshold {
			t.logger.Warnf("⚠️ 仓位不均衡检测: 净仓位=%.2f < -阈值=%.2f，跳过 BA 方向（会增加不均衡）",
				netPosition, -imbalanceThreshold)
			return fmt.Sprintf("仓位不均衡: 净仓位=%.2f, 跳过 BA 方向", netPosition)
		}
	}

	return "" // 仓位均衡，可以执行
}
