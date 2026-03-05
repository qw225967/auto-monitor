package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/model"

	binance_connector "github.com/binance/binance-connector-go"
)

// placeSpotOrder 现货下单
func (b *binance) placeSpotOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	apiKey, secretKey := b.getAPIKeys()

	side := "BUY"
	if req.Side == model.OrderSideSell {
		side = "SELL"
	}

	orderType := "MARKET"
	if req.Type == model.OrderTypeLimit {
		if req.Price <= 0 {
			return nil, fmt.Errorf("limit order requires price")
		}
		orderType = "LIMIT"
	}

	params := b.buildOrderParams(req, side, orderType)
	params = b.buildSignedParams(params, secretKey)

	formData := buildFormData(params)
	apiURL := fmt.Sprintf("%s%s", constants.BinanceRestBaseSpotUrl, constants.BinanceSpotOrderPath)

	headers := buildHeaders(apiKey)
	headers["Content-Type"] = "application/x-www-form-urlencoded"

	responseBody, err := restClient.DoPostWithHeaders(apiURL, formData, headers)
	if err != nil {
		return nil, fmt.Errorf("place spot order failed: %w", err)
	}

	return b.parseOrderResponse(responseBody, req)
}

// placeFuturesOrder 合约下单
func (b *binance) placeFuturesOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	apiKey, secretKey := b.getAPIKeys()

	side := "BUY"
	if req.Side == model.OrderSideSell {
		side = "SELL"
	}

	orderType := "MARKET"
	if req.Type == model.OrderTypeLimit {
		orderType = "LIMIT"
	}

	if req.Quantity <= 0 {
		return nil, fmt.Errorf("quantity must be greater than 0")
	}
	if orderType == "LIMIT" && req.Price <= 0 {
		return nil, fmt.Errorf("limit order requires price > 0")
	}

	params := b.buildOrderParams(req, side, orderType)
	if req.ReduceOnly {
		params["reduceOnly"] = "true"
	}
	if req.PositionSide != "" {
		positionSideStr := string(req.PositionSide)
		if positionSideStr == "LONG" || positionSideStr == "SHORT" || positionSideStr == "BOTH" {
			params["positionSide"] = positionSideStr
		}
	}

	params = b.buildSignedParams(params, secretKey)

	formData := buildFormData(params)
	apiURL := fmt.Sprintf("%s%s", constants.BinanceRestBaseUnifiedAccountUrl, constants.BinanceUnifiedAccountOrderPath)

	headers := buildHeaders(apiKey)
	headers["Content-Type"] = "application/x-www-form-urlencoded"

	responseBody, err := restClient.DoPostWithHeaders(apiURL, formData, headers)
	if err != nil {
		return nil, fmt.Errorf("place futures order failed: %w", err)
	}

	return b.parseOrderResponse(responseBody, req)
}

// buildOrderParams 构建订单参数
func (b *binance) buildOrderParams(req *model.PlaceOrderRequest, side, orderType string) map[string]string {
	timestamp := time.Now().UnixMilli()
	params := map[string]string{
		"symbol":     req.Symbol,
		"side":       side,
		"type":       orderType,
		"quantity":   formatQuantity(req.Quantity),
		"timestamp":  strconv.FormatInt(timestamp, 10),
		"recvWindow": constants.BinanceRecvWindow,
	}

	if orderType == "LIMIT" {
		params["price"] = formatPrice(req.Price)
		params["timeInForce"] = "GTC"
	}

	return params
}

// QueryFuturesOrder 查询合约订单状态（公开方法）
func (b *binance) QueryFuturesOrder(symbol, orderID string) (*model.Order, error) {
	return b.queryFuturesOrder(symbol, orderID)
}

// QuerySpotOrder 查询现货订单详情（使用官方 connector，由 SDK 处理 API Key 与签名，避免 -2014）
func (b *binance) QuerySpotOrder(symbol, orderID string) (*model.Order, error) {
	b.mu.RLock()
	spotClient := b.restAPISpotClient
	b.mu.RUnlock()

	if spotClient == nil {
		return nil, fmt.Errorf("spot API client not initialized")
	}

	orderIdInt, err := strconv.ParseInt(orderID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid orderId %q: %w", orderID, err)
	}

	resp, err := spotClient.NewGetOrderService().
		Symbol(symbol).
		OrderId(orderIdInt).
		Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("query spot order failed: %w", err)
	}

	order := binanceGetOrderResponseToModel(resp)
	// 现货订单接口不返回 fee，用 myTrades 按 orderId 拉取成交并汇总手续费
	if fee, err := b.fetchSpotOrderFee(spotClient, symbol, orderIdInt); err == nil {
		order.Fee = fee
	}
	return order, nil
}

// fetchSpotOrderFee 通过 GET /api/v3/myTrades?orderId=xxx 汇总该订单的手续费
func (b *binance) fetchSpotOrderFee(spotClient *binance_connector.Client, symbol string, orderId int64) (float64, error) {
	trades, err := spotClient.NewGetMyTradesService().
		Symbol(symbol).
		OrderId(orderId).
		Do(context.Background())
	if err != nil {
		return 0, err
	}
	var totalFee float64
	for _, t := range trades {
		commission, _ := strconv.ParseFloat(t.Commission, 64)
		if commission < 0 {
			commission = -commission
		}
		totalFee += commission
	}
	return totalFee, nil
}

// binanceGetOrderResponseToModel 将 connector GetOrderResponse 转为 model.Order
func binanceGetOrderResponseToModel(r *binance_connector.GetOrderResponse) *model.Order {
	price, _ := strconv.ParseFloat(r.Price, 64)
	origQty, _ := strconv.ParseFloat(r.OrigQty, 64)
	executedQty, _ := strconv.ParseFloat(r.ExecutedQty, 64)
	cumQuote, _ := strconv.ParseFloat(r.CummulativeQuoteQty, 64)
	avgPrice := 0.0
	if executedQty > 0 && cumQuote > 0 {
		avgPrice = cumQuote / executedQty
	}

	var orderSide model.OrderSide
	if r.Side == "BUY" {
		orderSide = model.OrderSideBuy
	} else {
		orderSide = model.OrderSideSell
	}
	var orderType model.OrderType
	if r.Type == "MARKET" {
		orderType = model.OrderTypeMarket
	} else {
		orderType = model.OrderTypeLimit
	}
	var orderStatus model.OrderStatus
	switch r.Status {
	case "NEW":
		orderStatus = model.OrderStatusNew
	case "PARTIALLY_FILLED":
		orderStatus = model.OrderStatusPartiallyFilled
	case "FILLED":
		orderStatus = model.OrderStatusFilled
	case "CANCELED":
		orderStatus = model.OrderStatusCanceled
	case "REJECTED":
		orderStatus = model.OrderStatusRejected
	case "EXPIRED":
		orderStatus = model.OrderStatusExpired
	default:
		orderStatus = model.OrderStatusNew
	}

	return &model.Order{
		OrderID:     strconv.FormatInt(r.OrderId, 10),
		Symbol:      r.Symbol,
		Side:        orderSide,
		Type:        orderType,
		Status:      orderStatus,
		Quantity:    origQty,
		Price:       price,
		FilledQty:   executedQty,
		FilledPrice: avgPrice,
		Fee:         0,
		CreateTime:  time.Unix(int64(r.Time)/1000, 0),
		UpdateTime:  time.Unix(int64(r.UpdateTime)/1000, 0),
	}
}

// queryFuturesOrder 查询合约订单状态（内部实现）
func (b *binance) queryFuturesOrder(symbol, orderID string) (*model.Order, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	apiKey, secretKey := b.getAPIKeys()
	if apiKey == "" {
		return nil, fmt.Errorf("binance API key is empty (check config or key passed to NewBinance); cannot query order")
	}

	timestamp := time.Now().UnixMilli()
	params := map[string]string{
		"symbol":     symbol,
		"orderId":    orderID,
		"timestamp":  strconv.FormatInt(timestamp, 10),
		"recvWindow": constants.BinanceRecvWindow,
	}

	params = b.buildSignedParams(params, secretKey)
	queryStr := buildQueryString(params)
	apiURL := fmt.Sprintf("%s%s?%s", constants.BinanceRestBaseUnifiedAccountUrl, constants.BinanceUnifiedAccountOrderPath, queryStr)

	headers := buildHeaders(apiKey)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("query futures order failed: %w", err)
	}

	// 复用 parseOrderResponse 解析订单响应
	return b.parseOrderResponse(responseBody, nil)
}

// buildSignedParams 构建带签名的参数
func (b *binance) buildSignedParams(params map[string]string, secretKey string) map[string]string {
	queryString := buildQueryString(params)
	signature := signRequest(queryString, secretKey)
	params["signature"] = signature
	return params
}

// parseOrderResponse 解析下单响应
func (b *binance) parseOrderResponse(responseBody string, req *model.PlaceOrderRequest) (*model.Order, error) {
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var orderResp struct {
		OrderID     interface{} `json:"orderId"`
		Symbol      string      `json:"symbol"`
		Side        string      `json:"side"`
		Type        string      `json:"type"`
		Status      string      `json:"status"`
		Price       string      `json:"price"`
		AvgPrice    string      `json:"avgPrice"`
		OrigQty     string      `json:"origQty"`
		ExecutedQty string      `json:"executedQty"`
		Time        int64       `json:"time"`
		UpdateTime  int64       `json:"updateTime"`
	}

	if err := json.Unmarshal([]byte(responseBody), &orderResp); err != nil {
		return nil, fmt.Errorf("parse order response failed: %w", err)
	}

	orderID := ""
	switch v := orderResp.OrderID.(type) {
	case float64:
		orderID = strconv.FormatInt(int64(v), 10)
	case string:
		orderID = v
	default:
		orderID = fmt.Sprintf("%v", v)
	}

	price, _ := strconv.ParseFloat(orderResp.Price, 64)
	avgPrice, _ := strconv.ParseFloat(orderResp.AvgPrice, 64)
	quantity, _ := strconv.ParseFloat(orderResp.OrigQty, 64)
	filledQty, _ := strconv.ParseFloat(orderResp.ExecutedQty, 64)

	var orderSide model.OrderSide
	if orderResp.Side == "BUY" {
		orderSide = model.OrderSideBuy
	} else {
		orderSide = model.OrderSideSell
	}

	var orderType model.OrderType
	if orderResp.Type == "MARKET" {
		orderType = model.OrderTypeMarket
	} else {
		orderType = model.OrderTypeLimit
	}

	var orderStatus model.OrderStatus
	switch orderResp.Status {
	case "NEW":
		orderStatus = model.OrderStatusNew
	case "PARTIALLY_FILLED":
		orderStatus = model.OrderStatusPartiallyFilled
	case "FILLED":
		orderStatus = model.OrderStatusFilled
	case "CANCELED":
		orderStatus = model.OrderStatusCanceled
	case "REJECTED":
		orderStatus = model.OrderStatusRejected
	case "EXPIRED":
		orderStatus = model.OrderStatusExpired
	default:
		orderStatus = model.OrderStatusNew
	}

	createTime := time.Unix(orderResp.Time/1000, 0)
	updateTime := time.Unix(orderResp.UpdateTime/1000, 0)

	return &model.Order{
		OrderID:     orderID,
		Symbol:      orderResp.Symbol,
		Side:        orderSide,
		Type:        orderType,
		Status:      orderStatus,
		Quantity:    quantity,
		Price:       price,
		FilledQty:   filledQty,
		FilledPrice: avgPrice,
		Fee:         0,
		CreateTime:  createTime,
		UpdateTime:  updateTime,
	}, nil
}
