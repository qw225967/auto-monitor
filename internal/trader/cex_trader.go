package trader

import (
	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// CexTrader CEX 交易所 Trader 适配器
// 包装 exchange.Exchange（binance, gate, bybit, bitget）
type CexTrader struct {
	exchange exchange.Exchange
	logger   *zap.SugaredLogger

	// 订阅管理
	subscribedSymbols map[string]bool // 已订阅的 symbol -> isFutures
	mu                sync.RWMutex

	// 价格回调转换
	priceCallback PriceCallback
}

// NewCexTrader 创建 CEX Trader 适配器
func NewCexTrader(ex exchange.Exchange) *CexTrader {
	if ex == nil {
		return nil
	}
	return &CexTrader{
		exchange:          ex,
		logger:            logger.GetLoggerInstance().Named("CexTrader").Sugar(),
		subscribedSymbols: make(map[string]bool),
	}
}

// GetType 获取 trader 类型
func (c *CexTrader) GetType() string {
	return c.exchange.GetType()
}

// Init 初始化连接
func (c *CexTrader) Init() error {
	return c.exchange.Init()
}

// Subscribe 订阅价格数据
// 对于 CEX，统一订阅到交易所实例
// marketType: "spot" 或 "futures"，为空时默认 "futures"
func (c *CexTrader) Subscribe(symbol string, marketType string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 判断是现货还是合约
	// 如果 marketType 为空，默认使用合约
	isFutures := true
	if marketType == "spot" {
		isFutures = false
	}

	// 检查是否已订阅
	if existingIsFutures, exists := c.subscribedSymbols[symbol]; exists {
		if existingIsFutures == isFutures {
			c.logger.Debugf("Symbol %s already subscribed (futures=%v)", symbol, isFutures)
			return nil
		}
		// 如果订阅类型不同，先取消订阅
		if existingIsFutures {
			c.exchange.UnsubscribeTicker([]string{}, []string{symbol})
		} else {
			c.exchange.UnsubscribeTicker([]string{symbol}, []string{})
		}
	}

	// 订阅到交易所
	var err error
	if isFutures {
		err = c.exchange.SubscribeTicker([]string{}, []string{symbol})
	} else {
		err = c.exchange.SubscribeTicker([]string{symbol}, []string{})
	}

	if err != nil {
		return err
	}

	c.subscribedSymbols[symbol] = isFutures
	c.logger.Debugf("Subscribed symbol %s (futures=%v)", symbol, isFutures)
	return nil
}

// Unsubscribe 取消订阅价格数据
// marketType: 市场类型（"spot" 或 "futures"），为空时默认 "futures"
func (c *CexTrader) Unsubscribe(symbol string, marketType string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	isFutures := true
	if marketType == "spot" {
		isFutures = false
	}

	existingIsFutures, exists := c.subscribedSymbols[symbol]
	if !exists {
		c.logger.Debugf("Symbol %s not subscribed", symbol)
		return nil
	}

	// 如果订阅类型不匹配，不取消订阅
	if existingIsFutures != isFutures {
		c.logger.Debugf("Symbol %s subscribed as %v but unsubscribe as %v, skipping", symbol, existingIsFutures, isFutures)
		return nil
	}

	// 取消订阅
	var err error
	if isFutures {
		err = c.exchange.UnsubscribeTicker([]string{}, []string{symbol})
	} else {
		err = c.exchange.UnsubscribeTicker([]string{symbol}, []string{})
	}

	if err != nil {
		return err
	}

	delete(c.subscribedSymbols, symbol)
	c.logger.Debugf("Unsubscribed symbol %s (futures=%v)", symbol, isFutures)
	return nil
}

// ExecuteOrder 执行交易订单（包含订单详情查询）
func (c *CexTrader) ExecuteOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	if req == nil {
		return nil, fmt.Errorf("order request is nil")
	}

	exchangeStart := time.Now()
	order, err := c.exchange.PlaceOrder(req)
	exchangeDuration := time.Since(exchangeStart)

	if err != nil {
		return nil, fmt.Errorf("exchange place order failed: %w", err)
	}

	if order != nil {
		c.logger.Infof("💱 [交易所] 下单成功 | %s %s %.0f, orderID=%s, 耗时=%.0fms",
			req.Side, req.Symbol, req.Quantity, order.OrderID, exchangeDuration.Seconds()*1000)

		// 🔥 市价单立即查询订单详情，获取实际成交信息
		if req.Type == model.OrderTypeMarket && order.OrderID != "" {
			// 等待100ms让订单成交
			time.Sleep(100 * time.Millisecond)

			queryStart := time.Now()
			detailedOrder, err := c.queryOrderDetails(req.Symbol, order.OrderID, req.MarketType)
			queryDuration := time.Since(queryStart)

			if err != nil {
				c.logger.Warnf("  ⚠️ 查询订单详情失败: %v, 查询耗时=%.0fms", err, queryDuration.Seconds()*1000)
			} else if detailedOrder != nil {
				order = detailedOrder // 使用详细订单信息
				c.logger.Infof("  ✅ 订单详情查询成功 | 成交数量=%.6f, 成交价=%.6f, 手续费=%.6f, 查询耗时=%.0fms",
					order.FilledQty, order.FilledPrice, order.Fee, queryDuration.Seconds()*1000)
			}
		}
	}

	return order, nil
}

// QueryOrderDetails 查询订单详情（供成交后异步拉取完整成交信息）
// 根据 marketType 分发到 QuerySpotOrder 或 QueryFuturesOrder
func (c *CexTrader) QueryOrderDetails(symbol, orderID string, marketType model.MarketType) (*model.Order, error) {
	return c.queryOrderDetails(symbol, orderID, marketType)
}

// queryOrderDetails 内部查询实现
func (c *CexTrader) queryOrderDetails(symbol, orderID string, marketType model.MarketType) (*model.Order, error) {
	if marketType == model.MarketTypeSpot {
		if ex, ok := c.exchange.(interface {
			QuerySpotOrder(symbol, orderID string) (*model.Order, error)
		}); ok {
			return ex.QuerySpotOrder(symbol, orderID)
		}
		return nil, fmt.Errorf("exchange does not support QuerySpotOrder")
	}

	if ex, ok := c.exchange.(interface {
		QueryFuturesOrder(symbol, orderID string) (*model.Order, error)
	}); ok {
		return ex.QueryFuturesOrder(symbol, orderID)
	}
	return nil, fmt.Errorf("exchange does not support QueryFuturesOrder")
}

// SetPriceCallback 设置价格数据回调函数
// 注意：仅保存 callback，不再调用 exchange.SetTickerCallback，以避免覆盖 initTraders 中为 handlePriceMsgChan 设置的唯一回调；
// CEX 价格现由 handlePriceMsgChan 按 exchangeType 统一路由到各 trigger 的 sourceXPriceChan。
func (c *CexTrader) SetPriceCallback(callback PriceCallback) {
	c.priceCallback = callback
}

// GetBalance 获取账户余额（单个币种）
func (c *CexTrader) GetBalance() (*model.Balance, error) {
	return c.exchange.GetBalance()
}

// CalculateSlippage 计算滑点
func (c *CexTrader) CalculateSlippage(symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64) {
	return c.exchange.CalculateSlippage(symbol, amount, isFutures, side, slippageLimit)
}

// GetOrderBook 获取订单簿
func (c *CexTrader) GetOrderBook(symbol string, isFutures bool) (bids [][]string, asks [][]string, err error) {
	if isFutures {
		return c.exchange.GetFuturesOrderBook(symbol)
	}
	return c.exchange.GetSpotOrderBook(symbol)
}

// GetPosition 获取持仓
func (c *CexTrader) GetPosition(symbol string) (*model.Position, error) {
	return c.exchange.GetPosition(symbol)
}

// GetPositions 获取所有持仓
func (c *CexTrader) GetPositions() ([]*model.Position, error) {
	return c.exchange.GetPositions()
}

// GetAllBalances 获取所有币种的余额
func (c *CexTrader) GetAllBalances() (map[string]*model.Balance, error) {
	return c.exchange.GetAllBalances()
}

// GetSpotBalances 获取现货账户余额
func (c *CexTrader) GetSpotBalances() (map[string]*model.Balance, error) {
	return c.exchange.GetSpotBalances()
}

// GetFuturesBalances 获取合约账户余额
func (c *CexTrader) GetFuturesBalances() (map[string]*model.Balance, error) {
	return c.exchange.GetFuturesBalances()
}

// GetExchange 获取底层 Exchange 实例（用于向后兼容）
func (c *CexTrader) GetExchange() exchange.Exchange {
	return c.exchange
}
