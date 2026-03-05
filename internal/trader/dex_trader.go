package trader

import (
	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
	"sync"

	"go.uber.org/zap"
)

// DexTrader DEX 交易所 Trader 适配器
// 包装 exchange.Exchange（aster, hyperliquid, lighter）
type DexTrader struct {
	exchange exchange.Exchange
	logger   *zap.SugaredLogger

	// 订阅管理
	subscribedSymbols map[string]bool // 已订阅的 symbol -> isFutures
	mu                sync.RWMutex

	// 价格回调转换
	priceCallback PriceCallback
}

// NewDexTrader 创建 DEX Trader 适配器
func NewDexTrader(ex exchange.Exchange) *DexTrader {
	if ex == nil {
		return nil
	}
	return &DexTrader{
		exchange:          ex,
		logger:            logger.GetLoggerInstance().Named("DexTrader").Sugar(),
		subscribedSymbols: make(map[string]bool),
	}
}

// GetType 获取 trader 类型
func (d *DexTrader) GetType() string {
	return d.exchange.GetType()
}

// Init 初始化连接
func (d *DexTrader) Init() error {
	return d.exchange.Init()
}

// Subscribe 订阅价格数据
// 对于 DEX，统一订阅到交易所实例
// marketType: "spot" 或 "futures"，为空时默认 "futures"
func (d *DexTrader) Subscribe(symbol string, marketType string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 判断是现货还是合约
	isFutures := true
	if marketType == "spot" {
		isFutures = false
	}

	// 检查是否已订阅
	if existingIsFutures, exists := d.subscribedSymbols[symbol]; exists {
		if existingIsFutures == isFutures {
			d.logger.Debugf("Symbol %s already subscribed (futures=%v)", symbol, isFutures)
			return nil
		}
		// 如果订阅类型不同，先取消订阅
		if existingIsFutures {
			d.exchange.UnsubscribeTicker([]string{}, []string{symbol})
		} else {
			d.exchange.UnsubscribeTicker([]string{symbol}, []string{})
		}
	}

	// 订阅到交易所
	var err error
	if isFutures {
		err = d.exchange.SubscribeTicker([]string{}, []string{symbol})
	} else {
		err = d.exchange.SubscribeTicker([]string{symbol}, []string{})
	}

	if err != nil {
		return err
	}

	d.subscribedSymbols[symbol] = isFutures
	d.logger.Debugf("Subscribed symbol %s (futures=%v)", symbol, isFutures)
	return nil
}

// Unsubscribe 取消订阅价格数据
// marketType: 市场类型（"spot" 或 "futures"），为空时默认 "futures"
func (d *DexTrader) Unsubscribe(symbol string, marketType string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	isFutures := true
	if marketType == "spot" {
		isFutures = false
	}

	existingIsFutures, exists := d.subscribedSymbols[symbol]
	if !exists {
		d.logger.Debugf("Symbol %s not subscribed", symbol)
		return nil
	}

	// 如果订阅类型不匹配，不取消订阅
	if existingIsFutures != isFutures {
		d.logger.Debugf("Symbol %s subscribed as %v but unsubscribe as %v, skipping", symbol, existingIsFutures, isFutures)
		return nil
	}

	// 取消订阅
	var err error
	if isFutures {
		err = d.exchange.UnsubscribeTicker([]string{}, []string{symbol})
	} else {
		err = d.exchange.UnsubscribeTicker([]string{symbol}, []string{})
	}

	if err != nil {
		return err
	}

	delete(d.subscribedSymbols, symbol)
	d.logger.Debugf("Unsubscribed symbol %s (futures=%v)", symbol, isFutures)
	return nil
}

// ExecuteOrder 执行交易订单
func (d *DexTrader) ExecuteOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	return d.exchange.PlaceOrder(req)
}

// SetPriceCallback 设置价格数据回调函数
// 注意：仅保存 callback，不再调用 exchange.SetTickerCallback，以避免覆盖 initTraders 中为 handlePriceMsgChan 设置的唯一回调；
// DEX 价格现由 handlePriceMsgChan 按 exchangeType 统一路由到各 trigger 的 sourceXPriceChan。
func (d *DexTrader) SetPriceCallback(callback PriceCallback) {
	d.priceCallback = callback
}

// GetBalance 获取账户余额（单个币种）
func (d *DexTrader) GetBalance() (*model.Balance, error) {
	return d.exchange.GetBalance()
}

// CalculateSlippage 计算滑点
func (d *DexTrader) CalculateSlippage(symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64) {
	return d.exchange.CalculateSlippage(symbol, amount, isFutures, side, slippageLimit)
}

// GetOrderBook 获取订单簿
func (d *DexTrader) GetOrderBook(symbol string, isFutures bool) (bids [][]string, asks [][]string, err error) {
	if isFutures {
		return d.exchange.GetFuturesOrderBook(symbol)
	}
	return d.exchange.GetSpotOrderBook(symbol)
}

// GetPosition 获取持仓
func (d *DexTrader) GetPosition(symbol string) (*model.Position, error) {
	return d.exchange.GetPosition(symbol)
}

// GetPositions 获取所有持仓
func (d *DexTrader) GetPositions() ([]*model.Position, error) {
	return d.exchange.GetPositions()
}

// GetAllBalances 获取所有币种的余额
func (d *DexTrader) GetAllBalances() (map[string]*model.Balance, error) {
	return d.exchange.GetAllBalances()
}

// GetSpotBalances 获取现货账户余额
func (d *DexTrader) GetSpotBalances() (map[string]*model.Balance, error) {
	return d.exchange.GetSpotBalances()
}

// GetFuturesBalances 获取合约账户余额
func (d *DexTrader) GetFuturesBalances() (map[string]*model.Balance, error) {
	return d.exchange.GetFuturesBalances()
}

// GetExchange 获取底层 Exchange 实例（用于向后兼容）
func (d *DexTrader) GetExchange() exchange.Exchange {
	return d.exchange
}
