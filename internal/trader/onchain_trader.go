package trader

import (
	"auto-arbitrage/constants"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/onchain"
	"auto-arbitrage/internal/utils/logger"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"
)

// OnchainTraderImpl 链上 Trader 适配器实现
// 包装 onchain.OnchainClient
type OnchainTraderImpl struct {
	client onchain.OnchainClient
	logger *zap.SugaredLogger

	// 订阅管理（链上每个实例独立 StartSwap）
	swapInfo    *model.SwapInfo
	swapRunning bool
	mu          sync.RWMutex

	// 价格回调转换
	priceCallback PriceCallback

	// 类型信息
	traderType string // 如 "onchain:56"
}

// NewOnchainTrader 创建链上 Trader 适配器
func NewOnchainTrader(client onchain.OnchainClient, traderType string) *OnchainTraderImpl {
	if client == nil {
		return nil
	}
	return &OnchainTraderImpl{
		client:      client,
		logger:      logger.GetLoggerInstance().Named("OnchainTrader").Sugar(),
		swapRunning: false,
		traderType:  traderType,
	}
}

// GetType 获取 trader 类型
func (o *OnchainTraderImpl) GetType() string {
	return o.traderType
}

// Init 初始化连接
func (o *OnchainTraderImpl) Init() error {
	return o.client.Init()
}

// Subscribe 订阅价格数据
// 对于链上，每个实例独立调用 StartSwap
// marketType: 链上不需要 marketType，忽略此参数
func (o *OnchainTraderImpl) Subscribe(symbol string, marketType string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.swapRunning {
		o.logger.Debugf("Swap already running for symbol %s", symbol)
		return nil
	}

	// 注意：链上的 Subscribe 需要先设置 swapInfo
	// 如果 swapInfo 未设置，这里无法启动
	if o.swapInfo == nil {
		o.logger.Warnf("SwapInfo not set, cannot start swap for symbol %s", symbol)
		return nil
	}

	// 启动 swap
	o.client.StartSwap(o.swapInfo)
	o.swapRunning = true
	o.logger.Debugf("Started swap for symbol %s", symbol)
	return nil
}

// Unsubscribe 取消订阅价格数据
// 对于链上，无法直接取消，只能停止 swap（但 OnchainClient 没有提供停止方法）
// marketType: 链上不需要 marketType，忽略此参数
func (o *OnchainTraderImpl) Unsubscribe(symbol string, marketType string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.swapRunning {
		o.logger.Debugf("Swap not running for symbol %s", symbol)
		return nil
	}

	// 注意：OnchainClient 没有提供停止 swap 的方法
	// 这里只是标记为未运行，实际的 swap 可能还在运行
	o.swapRunning = false
	o.logger.Debugf("Marked swap as stopped for symbol %s (actual swap may still be running)", symbol)
	return nil
}

// ExecuteOrder 执行交易订单
// 链上不支持直接下单，需要通过 BroadcastSwapTx
func (o *OnchainTraderImpl) ExecuteOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	// 链上不支持 Exchange 风格的订单
	// 需要使用 BroadcastSwapTx 方法
	return nil, nil
}

// SetPriceCallback 设置价格数据回调函数
func (o *OnchainTraderImpl) SetPriceCallback(callback PriceCallback) {
	o.priceCallback = callback

	// 将链上的 PriceCallback 转换为统一的 PriceCallback
	o.client.SetPriceCallback(func(price *model.ChainPriceInfo) {
		if o.priceCallback != nil {
			o.priceCallback(price.CoinSymbol, PriceData{
				ChainPrice: price,
			})
		}
	})
}

// GetBalance 获取账户余额（单个币种）
// 将 TokenBalance 转换为 Balance
func (o *OnchainTraderImpl) GetBalance() (*model.Balance, error) {
	tokenBalance, err := o.client.GetBalance()
	if err != nil {
		return nil, err
	}

	// 转换 TokenBalance 为 Balance
	balance, err := strconv.ParseFloat(tokenBalance.Balance, 64)
	if err != nil {
		return nil, err
	}

	return &model.Balance{
		Asset:      tokenBalance.TokenSymbol,
		Available:  balance,
		Locked:     0, // 链上余额没有锁定概念
		Total:      balance,
		UpdateTime: time.Now(),
	}, nil
}

// CalculateSlippage 计算滑点（链上不支持，返回 0）
func (o *OnchainTraderImpl) CalculateSlippage(symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64) {
	// 链上滑点计算需要从 GetLatestSwapTx 获取
	// 这里简化处理，返回 0
	return 0, 0
}

// GetOrderBook 获取订单簿（链上不支持）
func (o *OnchainTraderImpl) GetOrderBook(symbol string, isFutures bool) (bids [][]string, asks [][]string, err error) {
	return nil, nil, nil
}

// GetPosition 获取持仓（链上不支持）
func (o *OnchainTraderImpl) GetPosition(symbol string) (*model.Position, error) {
	return nil, nil
}

// GetPositions 获取所有持仓（链上不支持）
func (o *OnchainTraderImpl) GetPositions() ([]*model.Position, error) {
	return nil, nil
}

// GetAllBalances 获取所有币种的余额
func (o *OnchainTraderImpl) GetAllBalances() (map[string]*model.Balance, error) {
	// 使用 GetAllTokenBalances 获取所有余额
	// 需要钱包地址和链ID，这里简化处理
	swapInfo := o.GetSwapInfo()
	if swapInfo == nil {
		return nil, nil
	}

	tokenAssets, err := o.client.GetAllTokenBalances(swapInfo.WalletAddress, swapInfo.ChainIndex, false)
	if err != nil {
		return nil, err
	}

	balances := make(map[string]*model.Balance)
	for _, asset := range tokenAssets {
		balance, err := strconv.ParseFloat(asset.Balance, 64)
		if err != nil {
			continue
		}

		balances[asset.Symbol] = &model.Balance{
			Asset:      asset.Symbol,
			Available:  balance,
			Locked:     0,
			Total:      balance,
			UpdateTime: time.Now(),
		}
	}

	return balances, nil
}

// GetSpotBalances 获取现货账户余额（链上没有现货和合约的区别，返回所有余额）
func (o *OnchainTraderImpl) GetSpotBalances() (map[string]*model.Balance, error) {
	return o.GetAllBalances()
}

// GetFuturesBalances 获取合约账户余额（链上没有现货和合约的区别，返回空）
func (o *OnchainTraderImpl) GetFuturesBalances() (map[string]*model.Balance, error) {
	// 链上没有合约概念，返回空余额
	return make(map[string]*model.Balance), nil
}

// OnchainTrader 接口实现

// StartSwap 链上启动 swap
func (o *OnchainTraderImpl) StartSwap(swapInfo *model.SwapInfo) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.swapInfo = swapInfo
	o.client.StartSwap(swapInfo)
	o.swapRunning = true
	o.logger.Debugf("Started swap with swapInfo: %+v", swapInfo)
}

// StopSwap 停止链上询价循环（trigger 删除时调用）
func (o *OnchainTraderImpl) StopSwap() {
	o.client.StopSwap()
	o.mu.Lock()
	o.swapRunning = false
	o.mu.Unlock()
}

// BroadcastSwapTx 链上广播交易
func (o *OnchainTraderImpl) BroadcastSwapTx(direction onchain.SwapDirection) (string, error) {
	return o.client.BroadcastSwapTx(direction)
}

// GetTxResult 链上查询交易结果
func (o *OnchainTraderImpl) GetTxResult(txHash, chainIndex string) (model.TradeResult, error) {
	return o.client.GetTxResult(txHash, chainIndex)
}

// WaitForFullTxResult 轮询链上交易结果直到拿到完整兑换记录（AmountIn/AmountOut 非空）或超时
func (o *OnchainTraderImpl) WaitForFullTxResult(txHash, chainIndex string, direction onchain.SwapDirection, timeout time.Duration) (*OnchainTradeResult, error) {
	deadline := time.Now().Add(timeout)
	interval := 200 * time.Millisecond
	for time.Now().Before(deadline) {
		result, err := o.GetTxResult(txHash, chainIndex)
		if err != nil && err.Error() != "pending" && err.Error() != "swapping" {
			return nil, err
		}
		if result.Status == constants.TradeStatusSuccess && result.AmountIn != "" && result.AmountOut != "" {
			return o.parseTradeResult(&result, direction)
		}
		if result.Status == constants.TradeStatusFailed {
			return nil, errors.New(result.ErrorMsg)
		}
		time.Sleep(interval)
	}
	// 超时前再查一次，若已 success 则用兜底解析返回
	result, _ := o.GetTxResult(txHash, chainIndex)
	if result.Status == constants.TradeStatusSuccess {
		return o.parseTradeResult(&result, direction)
	}
	return nil, fmt.Errorf("wait for full tx result timeout after %v", timeout)
}

// GetLatestSwapTx 获取最新的 Swap 交易数据
func (o *OnchainTraderImpl) GetLatestSwapTx() interface{} {
	return o.client.GetLatestSwapTx()
}

// GetSwapInfo 获取当前的 Swap 信息
func (o *OnchainTraderImpl) GetSwapInfo() *model.SwapInfo {
	return o.client.GetSwapInfo()
}

// UpdateSwapInfoAmount 更新 Swap 信息中的 Amount 字段
func (o *OnchainTraderImpl) UpdateSwapInfoAmount(amount string) {
	o.client.UpdateSwapInfoAmount(amount)
}

// UpdateSwapInfoDecimals 更新 Swap 信息中的 Decimals 字段
func (o *OnchainTraderImpl) UpdateSwapInfoDecimals(decimalsFrom, decimalsTo string) {
	o.client.UpdateSwapInfoDecimals(decimalsFrom, decimalsTo)
}

// UpdateSwapInfoSlippage 更新 Swap 信息中的 Slippage 字段
func (o *OnchainTraderImpl) UpdateSwapInfoSlippage(slippage string) {
	o.client.UpdateSwapInfoSlippage(slippage)
}

// ResetNonce 重置 nonce 缓存
func (o *OnchainTraderImpl) ResetNonce(walletAddress, chainIndex string) {
	o.client.ResetNonce(walletAddress, chainIndex)
}

// GetOnchainClient 获取底层 OnchainClient 实例（用于向后兼容）
func (o *OnchainTraderImpl) GetOnchainClient() onchain.OnchainClient {
	return o.client
}

// SetSwapInfo 设置 SwapInfo（用于在 Subscribe 之前设置）
func (o *OnchainTraderImpl) SetSwapInfo(swapInfo *model.SwapInfo) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.swapInfo = swapInfo
}

// ExecuteOnChain 执行链上交易（包含广播、等待确认、解析结果）
// 返回：交易哈希、链上交易结果、错误
func (o *OnchainTraderImpl) ExecuteOnChain(chainIndex string, direction onchain.SwapDirection) (string, *OnchainTradeResult, error) {
	// 1. 执行链上交易（使用缓存的 TxDetail）
	broadcastStart := time.Now()
	txHash, err := o.BroadcastSwapTx(direction)
	if err != nil {
		return "", nil, fmt.Errorf("failed to broadcast onchain transaction: %w", err)
	}
	broadcastDuration := time.Since(broadcastStart)
	o.logger.Infof("📡 [链上] 广播 | direction=%s, txHash=%s, 耗时=%.0fms", direction, txHash, broadcastDuration.Seconds()*1000)

	// 等待链上交易确认（持续等待直到成功/失败，设置30s兜底超时）
	timeout := o.getChainTimeout(chainIndex)
	o.logger.Infof("⏱️  [链上] 等待确认 | 持续等待中... (链=%s)", chainIndex)

	waitStart := time.Now()
	success, tradeResult, err := o.waitForTxConfirmation(txHash, chainIndex, timeout, direction)
	waitDuration := time.Since(waitStart)

	if !success {
		if swapInfo := o.GetSwapInfo(); swapInfo != nil {
			o.ResetNonce(swapInfo.WalletAddress, swapInfo.ChainIndex)
		}
		// 返回 txHash 以便追踪
		return txHash, nil, err
	}
	o.logger.Infof("✅ [链上] 确认成功 | txHash=%s, AmountIn=%.6f, AmountOut=%.6f, Gas=%.6f, 等待耗时=%.2fs",
		txHash, tradeResult.AmountInFloat, tradeResult.AmountOutFloat, tradeResult.GasFee, waitDuration.Seconds())

	return txHash, tradeResult, nil
}

func writeOnchainTraderDebug(location, message, hypothesisId string, data map[string]interface{}) {}

// ExecuteSwapWithAmount 使用指定的 amount 独立执行 swap（不影响全局 SwapInfo）
// 此方法会临时更新 SwapInfo.Amount，执行一次询价和广播，然后恢复原始值
func (o *OnchainTraderImpl) ExecuteSwapWithAmount(amount string, chainIndex string, direction onchain.SwapDirection) (string, *OnchainTradeResult, error) {
	// 1. 获取当前 SwapInfo 并保存原始 amount
	swapInfo := o.GetSwapInfo()
	if swapInfo == nil {
		return "", nil, fmt.Errorf("swap info not set")
	}

	originalAmount := swapInfo.Amount
	// #region agent log
	writeOnchainTraderDebug("onchain_trader.go:ExecuteSwapWithAmount", "entry", "H1", map[string]interface{}{"amount": amount, "chainIndex": chainIndex, "direction": fmt.Sprint(direction), "originalAmount": originalAmount})
	// #endregion
	o.logger.Debugf("ExecuteSwapWithAmount: 保存原始 amount=%s, 临时使用 amount=%s, direction=%s", originalAmount, amount, direction)

	// 2. 临时更新 amount
	o.UpdateSwapInfoAmount(amount)

	// 3. 确保恢复原始 amount（使用 defer 确保即使出错也会恢复）
	defer func() {
		o.UpdateSwapInfoAmount(originalAmount)
		o.logger.Debugf("ExecuteSwapWithAmount: 已恢复原始 amount=%s", originalAmount)
	}()

	// 4. 等待询价循环使用新的 amount 并生成新的 TxDetail
	// 询价循环每 500ms 执行一次，等待略大于一个周期确保询价完成
	// 注意：这里假设 swap 正在运行，如果未运行则可能无法获取新的 TxDetail
	time.Sleep(700 * time.Millisecond) // 等待略大于 500ms 的询价周期，确保询价完成

	// 5. 执行链上交易（使用更新后的 amount 生成的 TxDetail）
	// 如果询价失败或未完成，BroadcastSwapTx 会返回错误
	return o.ExecuteOnChain(chainIndex, direction)
}

// getChainTimeout 获取超时时间
func (o *OnchainTraderImpl) getChainTimeout(chainIndex string) time.Duration {
	return 30 * time.Second
}

// waitForTxConfirmation 等待交易确认（第一时间查到即返回，缩短轮询间隔）
func (o *OnchainTraderImpl) waitForTxConfirmation(txHash, chainIndex string, timeout time.Duration, direction onchain.SwapDirection) (bool, *OnchainTradeResult, error) {
	startTime := time.Now()
	deadline := time.Now().Add(timeout)
	pollInterval := 80 * time.Millisecond // 80ms 轮询，确认后尽快返回

	// 辅助：单次查询并处理结果，返回 (done, success, tradeResult, err)
	tryQuery := func() (done bool, success bool, tradeResult *OnchainTradeResult, err error) {
		result, qErr := o.GetTxResult(txHash, chainIndex)
		if qErr != nil {
			if qErr.Error() == "pending" || qErr.Error() == "swapping" {
				return false, false, nil, nil
			}
			return true, false, nil, qErr
		}
		if result.Status == constants.TradeStatusSuccess {
			tr, parseErr := o.parseTradeResult(&result, direction)
			if parseErr != nil {
				return true, false, nil, parseErr
			}
			// API 有时先返回 success 但 fromTokenDetails/toTokenDetails 暂为空：短时重试 2 次再兜底
			if tr.AmountInFloat == 0 && tr.AmountOutFloat == 0 {
				for retry := 0; retry < 2; retry++ {
					time.Sleep(1 * time.Second)
					result2, qErr2 := o.GetTxResult(txHash, chainIndex)
					if qErr2 != nil || result2.Status != constants.TradeStatusSuccess {
						continue
					}
					if result2.AmountIn != "" && result2.AmountOut != "" {
						tr2, parseErr2 := o.parseTradeResult(&result2, direction)
						if parseErr2 == nil && (tr2.AmountInFloat > 0 || tr2.AmountOutFloat > 0) {
							o.logger.Debugf("链上确认重试第 %d 次拿到数量 | txHash=%s", retry+1, txHash)
							return true, true, tr2, nil
						}
					}
				}
				o.logger.Warnf("链上确认 success 但数量仍为空，使用 swapInfo 计划数量兜底 | txHash=%s", txHash)
			}
			return true, true, tr, nil
		}
		if result.Status == constants.TradeStatusFailed {
			if swapInfo := o.GetSwapInfo(); swapInfo != nil {
				o.ResetNonce(swapInfo.WalletAddress, swapInfo.ChainIndex)
			}
			return true, false, nil, errors.New(result.ErrorMsg)
		}
		return false, false, nil, nil
	}

	// 1. 立即查一次，不等待（确认快时可直接返回）
	if done, success, tr, err := tryQuery(); done {
		if success {
			o.logger.Infof("✅ 确认成功 | txHash=%s, 耗时=%.0fms", txHash, time.Since(startTime).Seconds()*1000)
			return true, tr, nil
		}
		if err != nil {
			o.logger.Errorf("❌ 查询/交易失败: %v", err)
			return false, nil, err
		}
	}

	// 2. 未终态则按间隔轮询，确认后尽快返回
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		<-ticker.C
		if done, success, tr, err := tryQuery(); done {
			if success {
				o.logger.Infof("✅ 确认成功 | txHash=%s, 耗时=%.0fms", txHash, time.Since(startTime).Seconds()*1000)
				return true, tr, nil
			}
			if err != nil {
				o.logger.Errorf("❌ 查询/交易失败: %v", err)
				return false, nil, err
			}
		}
	}

	// 超时
	o.logger.Errorf("⏰ 超时 | txHash=%s, 已等待=%.1fs", txHash, time.Since(startTime).Seconds())
	if swapInfo := o.GetSwapInfo(); swapInfo != nil {
		o.ResetNonce(swapInfo.WalletAddress, swapInfo.ChainIndex)
	}
	return false, nil, errors.New("query broadcast info timeout")
}

// parseTradeResult 解析交易结果，提取详细的数量信息
// 🔥 关键修复：根据交易方向正确选择 decimals
func (o *OnchainTraderImpl) parseTradeResult(result *model.TradeResult, direction onchain.SwapDirection) (*OnchainTradeResult, error) {
	swapInfo := o.GetSwapInfo()
	if swapInfo == nil {
		return nil, errors.New("swap info is nil")
	}

	tradeResult := &OnchainTradeResult{}

	// 🔥 根据方向确定正确的 decimals
	// SwapInfo 配置为 USDT -> Coin（DecimalsFrom=USDT精度，DecimalsTo=Coin精度）
	// 买入 (USDT -> Coin): AmountIn=USDT, AmountOut=Coin
	// 卖出 (Coin -> USDT): AmountIn=Coin, AmountOut=USDT（与 SwapInfo 方向相反）
	var decimalsIn, decimalsOut string
	if direction == onchain.SwapDirectionBuy {
		// 买入：USDT -> Coin
		decimalsIn = swapInfo.DecimalsFrom // USDT decimals
		decimalsOut = swapInfo.DecimalsTo  // Coin decimals
	} else {
		// 卖出：Coin -> USDT（方向与 SwapInfo 相反）
		decimalsIn = swapInfo.DecimalsTo    // Coin decimals
		decimalsOut = swapInfo.DecimalsFrom // USDT decimals
	}

	// 转换 AmountIn（输入数量，链上 API 返回的原始字符串，通常为最小单位）
	if result.AmountIn != "" {
		amountInFloat, err := o.convertAmountFromDecimals(result.AmountIn, decimalsIn)
		if err == nil {
			tradeResult.AmountInFloat, _ = amountInFloat.Float64()
		} else {
			o.logger.Warnf("转换 AmountIn 失败: %v (raw=%s, decimals=%s)", err, result.AmountIn, decimalsIn)
		}
	} else {
		o.logger.Warnf("链上确认结果 AmountIn 为空，请检查 OKEx DEX API 返回的 fromTokenDetails.amount 或 ABI 解析")
	}

	// 转换 AmountOut（输出数量）
	if result.AmountOut != "" {
		amountOutFloat, err := o.convertAmountFromDecimals(result.AmountOut, decimalsOut)
		if err == nil {
			tradeResult.AmountOutFloat, _ = amountOutFloat.Float64()
		} else {
			o.logger.Warnf("转换 AmountOut 失败: %v (raw=%s, decimals=%s)", err, result.AmountOut, decimalsOut)
		}
	} else {
		o.logger.Warnf("链上确认结果 AmountOut 为空，请检查 OKEx DEX API 返回的 toTokenDetails.amount 或 ABI 解析")
	}

	// 根据方向确定币数量：买入得币=AmountOut，卖出耗币=AmountIn
	if direction == onchain.SwapDirectionBuy {
		// 买入：USDT -> Coin，得到的是 Coin
		tradeResult.CoinAmount = tradeResult.AmountOutFloat
	} else {
		// 卖出：Coin -> USDT，消耗的是 Coin
		tradeResult.CoinAmount = tradeResult.AmountInFloat
	}

	// 兜底：币数量为 0 时先用另一侧推导，再尝试 swapInfo
	if tradeResult.CoinAmount <= 0 {
		if direction == onchain.SwapDirectionBuy && tradeResult.AmountOutFloat > 0 {
			tradeResult.CoinAmount = tradeResult.AmountOutFloat
			o.logger.Debugf("CoinAmount 由 AmountOutFloat 兜底: %.6f", tradeResult.CoinAmount)
		} else if direction == onchain.SwapDirectionSell && tradeResult.AmountInFloat > 0 {
			tradeResult.CoinAmount = tradeResult.AmountInFloat
			o.logger.Debugf("CoinAmount 由 AmountInFloat 兜底: %.6f", tradeResult.CoinAmount)
		}
	}
	if tradeResult.CoinAmount <= 0 && swapInfo.Amount != "" {
		if amount, err := strconv.ParseFloat(swapInfo.Amount, 64); err == nil && amount > 0 {
			tradeResult.CoinAmount = amount
			o.logger.Debugf("使用 swapInfo.Amount 兜底: CoinAmount=%.6f", amount)
		}
	}

	// Gas/手续费：严格使用链上查询结果 TxFee（若为 wei 则换算为 native token 数量）
	if result.TxFee != "" {
		if fee, err := strconv.ParseFloat(result.TxFee, 64); err == nil && fee >= 0 {
			if fee > 1e10 {
				fee = fee / 1e18
			}
			tradeResult.GasFee = fee
			o.logger.Debugf("解析交易结果: TxFee=%s -> GasFee=%.6f", result.TxFee, tradeResult.GasFee)
		}
	}
	if tradeResult.GasFee < 0 {
		tradeResult.GasFee = 0
	}
	// 不再使用固定默认值 0.05：链上未返回 TxFee 时保持 GasFee=0，由前端展示为「--」或「未知」，避免误导
	if tradeResult.GasFee == 0 && (result.TxFee == "" || result.GasUsed == "") {
		o.logger.Debugf("链上未返回 TxFee/GasUsed，GasFee 保持为 0，请确认 OKEx DEX 交易状态 API 是否返回 txFee 或 gasUsed/gasPrice")
	}

	o.logger.Debugf("解析交易结果: direction=%s, AmountIn=%.6f, AmountOut=%.6f, CoinAmount=%.6f, GasFee=%.6f",
		direction, tradeResult.AmountInFloat, tradeResult.AmountOutFloat, tradeResult.CoinAmount, tradeResult.GasFee)

	return tradeResult, nil
}

// convertAmountFromDecimals 将最小单位金额转换为浮点数
func (o *OnchainTraderImpl) convertAmountFromDecimals(amountStr, decimalsStr string) (*big.Float, error) {
	if amountStr == "" {
		return nil, fmt.Errorf("amount cannot be empty")
	}

	amountInt := new(big.Int)
	if _, ok := amountInt.SetString(amountStr, 10); !ok {
		return nil, fmt.Errorf("invalid amount: %s", amountStr)
	}

	decimals := 0
	if decimalsStr != "" {
		var err error
		decimals, err = strconv.Atoi(decimalsStr)
		if err != nil {
			return nil, fmt.Errorf("invalid decimals: %v", err)
		}
	}

	denominator := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	if denominator.Cmp(big.NewInt(0)) == 0 {
		return nil, fmt.Errorf("decimals denominator cannot be zero")
	}

	amountFloat := new(big.Float).SetPrec(256).SetInt(amountInt)
	denominatorFloat := new(big.Float).SetPrec(256).SetInt(denominator)
	result := new(big.Float).SetPrec(256)
	result.Quo(amountFloat, denominatorFloat)
	return result, nil
}
