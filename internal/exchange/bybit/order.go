package bybit

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/model"
)

// placeSpotOrder 现货下单
func (b *bybit) placeSpotOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	apiKey, secretKey := b.getAPIKeys()

	side := "Buy"
	if req.Side == model.OrderSideSell {
		side = "Sell"
	}

	orderType := "Market"
	if req.Type == model.OrderTypeLimit {
		if req.Price <= 0 {
			return nil, fmt.Errorf("limit order requires price")
		}
		orderType = "Limit"
	}

	// 构建请求参数
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"

	orderParams := map[string]interface{}{
		"category":  "spot",
		"symbol":    req.Symbol,
		"side":      side,
		"orderType": orderType,
		"qty":       formatQuantity(req.Quantity),
	}
	// 现货市价单默认按「计价(quote)」：qty 会被当作 USDT，导致实际买入量 = qty/price。开仓需按「币数量(base)」下单，显式指定 baseCoin
	if orderType == "Market" {
		orderParams["marketUnit"] = "baseCoin"
	}
	if orderType == "Limit" {
		orderParams["price"] = formatPrice(req.Price)
		orderParams["timeInForce"] = "GTC"
	}

	// 生成 JSON body
	jsonBody, _ := json.Marshal(orderParams)
	requestBody := string(jsonBody)

	// 生成签名
	signature := signRequest(timestamp, apiKey, recvWindow, requestBody, secretKey)

	// 发送 HTTP POST 请求
	apiURL := fmt.Sprintf("%s%s", constants.BybitRestBaseUrl, constants.BybitSpotOrderPath)
	headers := buildHeaders(apiKey, timestamp, signature, recvWindow)

	responseBody, err := restClient.DoPostWithHeaders(apiURL, requestBody, headers)
	if err != nil {
		return nil, fmt.Errorf("place spot order failed: %w", err)
	}

	// 解析响应
	return b.parseOrderResponse(responseBody)
}

// placeFuturesOrder 合约下单
func (b *bybit) placeFuturesOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	apiKey, secretKey := b.getAPIKeys()

	side := "Buy"
	if req.Side == model.OrderSideSell {
		side = "Sell"
	}

	orderType := "Market"
	if req.Type == model.OrderTypeLimit {
		orderType = "Limit"
	}

	if req.Quantity <= 0 {
		return nil, fmt.Errorf("quantity must be greater than 0")
	}
	if orderType == "Limit" && req.Price <= 0 {
		return nil, fmt.Errorf("limit order requires price > 0")
	}

	// 构建请求参数
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"

	orderParams := map[string]interface{}{
		"category":  "linear",
		"symbol":    req.Symbol,
		"side":      side,
		"orderType": orderType,
		"qty":       formatQuantity(req.Quantity),
	}

	if orderType == "Limit" {
		orderParams["price"] = formatPrice(req.Price)
		orderParams["timeInForce"] = "GTC"
	}

	if req.ReduceOnly {
		orderParams["reduceOnly"] = true
	}

	if req.PositionSide != "" {
		// Bybit 使用 positionIdx: 0=单向持仓, 1=买方向, 2=卖方向
		positionIdx := 0
		if req.PositionSide == model.PositionSideLong {
			positionIdx = 1
		} else if req.PositionSide == model.PositionSideShort {
			positionIdx = 2
		}
		orderParams["positionIdx"] = positionIdx
	}

	// 生成 JSON body
	jsonBody, _ := json.Marshal(orderParams)
	requestBody := string(jsonBody)

	// 生成签名
	signature := signRequest(timestamp, apiKey, recvWindow, requestBody, secretKey)

	// 发送 HTTP POST 请求
	apiURL := fmt.Sprintf("%s%s", constants.BybitRestBaseUrl, constants.BybitFuturesOrderPath)
	headers := buildHeaders(apiKey, timestamp, signature, recvWindow)

	responseBody, err := restClient.DoPostWithHeaders(apiURL, requestBody, headers)
	if err != nil {
		return nil, fmt.Errorf("place futures order failed: %w", err)
	}

	// 解析响应
	return b.parseOrderResponse(responseBody)
}

// QueryFuturesOrder 查询合约订单详情
func (b *bybit) QueryFuturesOrder(symbol, orderID string) (*model.Order, error) {
	return b.queryOrder(symbol, orderID, "linear")
}

// QuerySpotOrder 查询现货订单详情
func (b *bybit) QuerySpotOrder(symbol, orderID string) (*model.Order, error) {
	return b.queryOrder(symbol, orderID, "spot")
}

// queryOrder 统一订单查询（category 区分现货/合约）
func (b *bybit) queryOrder(symbol, orderID, category string) (*model.Order, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	apiKey, secretKey := b.getAPIKeys()

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"

	queryString := fmt.Sprintf("category=%s&symbol=%s&orderId=%s", category, symbol, orderID)

	signature := signRequest(timestamp, apiKey, recvWindow, queryString, secretKey)

	apiURL := fmt.Sprintf("%s%s?%s", constants.BybitRestBaseUrl, constants.BybitOrderDetailPath, queryString)
	headers := buildHeaders(apiKey, timestamp, signature, recvWindow)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("query %s order failed: %w", category, err)
	}

	return b.parseQueryOrderResponse(responseBody)
}

// parseQueryOrderResponse 解析订单查询响应（/v5/order/realtime 返回 list 数组）
func (b *bybit) parseQueryOrderResponse(responseBody string) (*model.Order, error) {
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				OrderId      string `json:"orderId"`
				Symbol       string `json:"symbol"`
				Side         string `json:"side"`
				OrderType    string `json:"orderType"`
				OrderStatus  string `json:"orderStatus"`
				Price        string `json:"price"`
				Qty          string `json:"qty"`
				AvgPrice     string `json:"avgPrice"`
				CumExecQty   string `json:"cumExecQty"`
				CumExecValue string `json:"cumExecValue"`
				CumExecFee   string `json:"cumExecFee"`
				CreatedTime  string `json:"createdTime"`
				UpdatedTime  string `json:"updatedTime"`
			} `json:"list"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse query order response failed: %w", err)
	}

	if resp.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error: code=%d, msg=%s", resp.RetCode, resp.RetMsg)
	}

	if len(resp.Result.List) == 0 {
		return nil, fmt.Errorf("order not found: %s", responseBody)
	}

	item := resp.Result.List[0]

	price, _ := strconv.ParseFloat(item.Price, 64)
	avgPrice, _ := strconv.ParseFloat(item.AvgPrice, 64)
	quantity, _ := strconv.ParseFloat(item.Qty, 64)
	filledQty, _ := strconv.ParseFloat(item.CumExecQty, 64)
	fee, _ := strconv.ParseFloat(item.CumExecFee, 64)
	if fee < 0 {
		fee = -fee
	}

	var orderSide model.OrderSide
	if item.Side == "Buy" {
		orderSide = model.OrderSideBuy
	} else {
		orderSide = model.OrderSideSell
	}

	var orderType model.OrderType
	if item.OrderType == "Market" {
		orderType = model.OrderTypeMarket
	} else {
		orderType = model.OrderTypeLimit
	}

	var orderStatus model.OrderStatus
	switch item.OrderStatus {
	case "New", "Created", "Untriggered":
		orderStatus = model.OrderStatusNew
	case "PartiallyFilled", "PartiallyFilledCanceled":
		orderStatus = model.OrderStatusPartiallyFilled
	case "Filled":
		orderStatus = model.OrderStatusFilled
	case "Cancelled", "Deactivated":
		orderStatus = model.OrderStatusCanceled
	case "Rejected":
		orderStatus = model.OrderStatusRejected
	default:
		orderStatus = model.OrderStatusNew
	}

	createdTime, _ := strconv.ParseInt(item.CreatedTime, 10, 64)
	updatedTime, _ := strconv.ParseInt(item.UpdatedTime, 10, 64)

	return &model.Order{
		OrderID:     item.OrderId,
		Symbol:      item.Symbol,
		Side:        orderSide,
		Type:        orderType,
		Status:      orderStatus,
		Quantity:    quantity,
		Price:       price,
		FilledQty:   filledQty,
		FilledPrice: avgPrice,
		Fee:         fee,
		CreateTime:  time.Unix(createdTime/1000, 0),
		UpdateTime:  time.Unix(updatedTime/1000, 0),
	}, nil
}

// parseOrderResponse 解析下单响应
func (b *bybit) parseOrderResponse(responseBody string) (*model.Order, error) {
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var orderResp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			OrderId      string `json:"orderId"`
			OrderLinkId  string `json:"orderLinkId"`
			Symbol       string `json:"symbol"`
			Side         string `json:"side"`
			OrderType    string `json:"orderType"`
			Price        string `json:"price"`
			Qty          string `json:"qty"`
			OrderStatus  string `json:"orderStatus"`
			CumExecQty   string `json:"cumExecQty"`
			CumExecValue string `json:"cumExecValue"`
			AvgPrice     string `json:"avgPrice"`
			CreatedTime  string `json:"createdTime"`
			UpdatedTime  string `json:"updatedTime"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(responseBody), &orderResp); err != nil {
		return nil, fmt.Errorf("parse order response failed: %w", err)
	}

	if orderResp.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error: code=%d, msg=%s", orderResp.RetCode, orderResp.RetMsg)
	}

	price, _ := strconv.ParseFloat(orderResp.Result.Price, 64)
	avgPrice, _ := strconv.ParseFloat(orderResp.Result.AvgPrice, 64)
	quantity, _ := strconv.ParseFloat(orderResp.Result.Qty, 64)
	filledQty, _ := strconv.ParseFloat(orderResp.Result.CumExecQty, 64)

	var orderSide model.OrderSide
	if orderResp.Result.Side == "Buy" {
		orderSide = model.OrderSideBuy
	} else {
		orderSide = model.OrderSideSell
	}

	var orderType model.OrderType
	if orderResp.Result.OrderType == "Market" {
		orderType = model.OrderTypeMarket
	} else {
		orderType = model.OrderTypeLimit
	}

	var orderStatus model.OrderStatus
	switch orderResp.Result.OrderStatus {
	case "New", "Created":
		orderStatus = model.OrderStatusNew
	case "PartiallyFilled":
		orderStatus = model.OrderStatusPartiallyFilled
	case "Filled":
		orderStatus = model.OrderStatusFilled
	case "Cancelled":
		orderStatus = model.OrderStatusCanceled
	case "Rejected":
		orderStatus = model.OrderStatusRejected
	default:
		orderStatus = model.OrderStatusNew
	}

	createdTime, _ := strconv.ParseInt(orderResp.Result.CreatedTime, 10, 64)
	updatedTime, _ := strconv.ParseInt(orderResp.Result.UpdatedTime, 10, 64)

	return &model.Order{
		OrderID:     orderResp.Result.OrderId,
		Symbol:      orderResp.Result.Symbol,
		Side:        orderSide,
		Type:        orderType,
		Status:      orderStatus,
		Quantity:    quantity,
		Price:       price,
		FilledQty:   filledQty,
		FilledPrice: avgPrice,
		Fee:         0,
		CreateTime:  time.Unix(createdTime/1000, 0),
		UpdateTime:  time.Unix(updatedTime/1000, 0),
	}, nil
}
