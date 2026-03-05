package bitget

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/model"
)

// placeSpotOrder 现货下单
func (b *bitgetExchange) placeSpotOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	apiKey, secretKey, passphrase := b.getAPIKeys()

	side := "buy"
	if req.Side == model.OrderSideSell {
		side = "sell"
	}

	orderType := "market"
	limitPrice := req.Price
	if req.Type == model.OrderTypeLimit {
		if req.Price <= 0 {
			return nil, fmt.Errorf("limit order requires price")
		}
		orderType = "limit"
	}

	sizeStr := formatQuantity(req.Quantity)
	// Bitget 现货买入：改用限价单 + base 数量，保证与套保端精确一致；限价买 price=ask*1.002 提高成交概率
	// 限价买语义：成交价 ≤ 限价即成交；价格低于限价时以更优价成交，不会「没买入」；唯一风险是价格快速上涨超过限价导致不成交
	if side == "buy" && (orderType == "market" || (orderType == "limit" && limitPrice <= 0)) {
		_, asks, err := b.GetSpotOrderBook(req.Symbol)
		if err != nil {
			return nil, fmt.Errorf("bitget spot buy: get orderbook failed: %w", err)
		}
		if len(asks) == 0 || len(asks[0]) < 1 {
			return nil, fmt.Errorf("bitget spot buy: orderbook empty or invalid")
		}
		askPrice, e := strconv.ParseFloat(asks[0][0], 64)
		if e != nil || askPrice <= 0 {
			return nil, fmt.Errorf("bitget spot buy: invalid ask price from orderbook")
		}
		orderType = "limit"
		limitPrice = askPrice * 1.005 // 比 ask 高 0.5%，保证每次限价买都能成交
		// size 使用 base 数量，限价买 API 支持
	}

	// 构建请求体（V2 API）
	orderReq := map[string]interface{}{
		"symbol":    normalizeBitgetSymbol(req.Symbol, false),
		"side":      side,
		"orderType": orderType,
		"size":      sizeStr,
	}

	if orderType == "limit" {
		orderReq["price"] = formatPrice(limitPrice)
		orderReq["force"] = "ioc" //  immediate-or-cancel：立即成交或撤单，避免挂单残留
	}

	requestBody, _ := json.Marshal(orderReq)
	timestamp := getCurrentTimestamp()

	// 生成签名
	signature := signRequest(timestamp, "POST", constants.BitgetSpotOrderPath, "", string(requestBody), secretKey)

	// 发送 HTTP POST 请求
	apiURL := fmt.Sprintf("%s%s", constants.BitgetRestBaseUrl, constants.BitgetSpotOrderPath)
	headers := buildHeaders(apiKey, signature, timestamp, passphrase)

	responseBody, err := restClient.DoPostWithHeaders(apiURL, string(requestBody), headers)
	if err != nil {
		return nil, fmt.Errorf("place spot order failed: %w", err)
	}

	// 解析响应
	return b.parseSpotOrderResponse(responseBody)
}

// placeFuturesOrder 合约下单
func (b *bitgetExchange) placeFuturesOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	apiKey, secretKey, passphrase := b.getAPIKeys()

	// Bitget V2 合约 API：side 仅允许 "buy"/"sell"，开平用 tradeSide "open"/"close"
	// 文档：Open short: side=sell, tradeSide=open; Close long: side=buy, tradeSide=close; Close short: side=sell, tradeSide=close
	side := "buy"
	tradeSide := "open"
	if req.Side == model.OrderSideSell {
		side = "sell"
	}
	if req.ReduceOnly {
		tradeSide = "close"
		if req.Side == model.OrderSideBuy {
			side = "sell" // 平空
		} else {
			side = "buy" // 平多
		}
	}

	orderType := "market"
	if req.Type == model.OrderTypeLimit {
		orderType = "limit"
	}

	// 构建请求体（V2 API）
	orderReq := map[string]interface{}{
		"symbol":      normalizeBitgetSymbol(req.Symbol, true),
		"productType": "USDT-FUTURES", // V2 API 必需参数
		"marginMode":  "crossed",      // crossed 或 isolated
		"marginCoin":  "USDT",
		"size":        formatQuantity(req.Quantity),
		"side":        side,
		"tradeSide":   tradeSide,
		"orderType":   orderType,
	}

	if orderType == "limit" {
		orderReq["price"] = formatPrice(req.Price)
		orderReq["force"] = "gtc" // V2 API: gtc, post_only, fok, ioc
	}

	requestBody, _ := json.Marshal(orderReq)
	timestamp := getCurrentTimestamp()

	// 生成签名
	signature := signRequest(timestamp, "POST", constants.BitgetFuturesOrderPath, "", string(requestBody), secretKey)

	// 发送 HTTP POST 请求
	apiURL := fmt.Sprintf("%s%s", constants.BitgetRestBaseUrl, constants.BitgetFuturesOrderPath)
	headers := buildHeaders(apiKey, signature, timestamp, passphrase)

	responseBody, err := restClient.DoPostWithHeaders(apiURL, string(requestBody), headers)
	if err != nil {
		return nil, fmt.Errorf("place futures order failed: %w", err)
	}

	// 解析响应
	return b.parseFuturesOrderResponse(responseBody)
}

// QueryFuturesOrder 查询合约订单详情
func (b *bitgetExchange) QueryFuturesOrder(symbol, orderID string) (*model.Order, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	apiKey, secretKey, passphrase := b.getAPIKeys()

	queryString := fmt.Sprintf("symbol=%s&orderId=%s&productType=USDT-FUTURES",
		normalizeBitgetSymbol(symbol, true), orderID)
	timestamp := getCurrentTimestamp()

	signature := signRequest(timestamp, "GET", constants.BitgetFuturesOrderInfo, queryString, "", secretKey)

	apiURL := fmt.Sprintf("%s%s?%s", constants.BitgetRestBaseUrl, constants.BitgetFuturesOrderInfo, queryString)
	headers := buildHeaders(apiKey, signature, timestamp, passphrase)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("query futures order failed: %w", err)
	}

	return b.parseFuturesOrderDetailResponse(responseBody)
}

// QuerySpotOrder 查询现货订单详情
func (b *bitgetExchange) QuerySpotOrder(symbol, orderID string) (*model.Order, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	apiKey, secretKey, passphrase := b.getAPIKeys()

	queryString := fmt.Sprintf("symbol=%s&orderId=%s",
		normalizeBitgetSymbol(symbol, false), orderID)
	timestamp := getCurrentTimestamp()

	signature := signRequest(timestamp, "GET", constants.BitgetSpotOrderInfo, queryString, "", secretKey)

	apiURL := fmt.Sprintf("%s%s?%s", constants.BitgetRestBaseUrl, constants.BitgetSpotOrderInfo, queryString)
	headers := buildHeaders(apiKey, signature, timestamp, passphrase)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("query spot order failed: %w", err)
	}

	return b.parseSpotOrderDetailResponse(responseBody)
}

// parseFuturesOrderDetailResponse 解析合约订单查询响应
func (b *bitgetExchange) parseFuturesOrderDetailResponse(responseBody string) (*model.Order, error) {
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			OrderId   string `json:"orderId"`
			Symbol    string `json:"symbol"`
			Side      string `json:"side"`
			OrderType string `json:"orderType"`
			Price     string `json:"price"`
			Size      string `json:"size"`
			Status    string `json:"status"`
			State     string `json:"state"`      // v1 订单详情用 state
			FillPrice string `json:"fillPrice"`  // 旧版
			PriceAvg  string `json:"priceAvg"`   // V2 成交均价
			FillSize  string `json:"baseVolume"` // v2 部分返回
			FilledQty string `json:"filledQty"`  // v1 及 v2 部分返回：成交数量( base )
			Fee       string `json:"fee"`
			CTime     string `json:"cTime"`
			UTime     string `json:"uTime"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse futures order detail failed: %w", err)
	}

	if resp.Code != "00000" {
		return nil, fmt.Errorf("bitget API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	price, _ := strconv.ParseFloat(resp.Data.Price, 64)
	// V2 返回 priceAvg，旧版可能为 fillPrice
	fillPriceStr := resp.Data.PriceAvg
	if fillPriceStr == "" {
		fillPriceStr = resp.Data.FillPrice
	}
	fillPrice, _ := strconv.ParseFloat(fillPriceStr, 64)
	quantity, _ := strconv.ParseFloat(resp.Data.Size, 64)
	filledQtyStr := resp.Data.FilledQty
	if filledQtyStr == "" {
		filledQtyStr = resp.Data.FillSize
	}
	filledQty, _ := strconv.ParseFloat(filledQtyStr, 64)
	fee, _ := strconv.ParseFloat(resp.Data.Fee, 64)
	if fee < 0 {
		fee = -fee
	}

	var orderSide model.OrderSide
	if resp.Data.Side == "open_long" || resp.Data.Side == "close_short" || resp.Data.Side == "buy" {
		orderSide = model.OrderSideBuy
	} else {
		orderSide = model.OrderSideSell
	}

	var orderType model.OrderType
	if resp.Data.OrderType == "market" {
		orderType = model.OrderTypeMarket
	} else {
		orderType = model.OrderTypeLimit
	}

	statusOrState := resp.Data.Status
	if statusOrState == "" {
		statusOrState = resp.Data.State
	}
	var orderStatus model.OrderStatus
	switch statusOrState {
	case "new", "init", "not_trigger":
		orderStatus = model.OrderStatusNew
	case "partial_fill", "partial-fill":
		orderStatus = model.OrderStatusPartiallyFilled
	case "full_fill", "full-fill", "filled":
		orderStatus = model.OrderStatusFilled
	case "cancelled", "canceled":
		orderStatus = model.OrderStatusCanceled
	default:
		orderStatus = model.OrderStatusNew
	}

	createTime, _ := strconv.ParseInt(resp.Data.CTime, 10, 64)
	updateTime, _ := strconv.ParseInt(resp.Data.UTime, 10, 64)

	return &model.Order{
		OrderID:     resp.Data.OrderId,
		Symbol:      resp.Data.Symbol,
		Side:        orderSide,
		Type:        orderType,
		Status:      orderStatus,
		Quantity:    quantity,
		Price:       price,
		FilledQty:   filledQty,
		FilledPrice: fillPrice,
		Fee:         fee,
		CreateTime:  time.Unix(createTime/1000, 0),
		UpdateTime:  time.Unix(updateTime/1000, 0),
	}, nil
}

// parseSpotFeeDetail 从 V2 现货 feeDetail JSON 字符串中解析总手续费（取绝对值）
// 格式示例: {"newFees":{"t":-0.112,"r":-0.112,...},"BGB":{"totalFee":-0.0041,...}}
// 优先取 newFees.t，否则取任意子对象中的 totalFee
func parseSpotFeeDetail(feeDetail string) float64 {
	if feeDetail == "" {
		return 0
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(feeDetail), &raw); err != nil {
		return 0
	}
	// 优先 newFees.t（V2 文档：总手续费）
	if newFees, ok := raw["newFees"].(map[string]interface{}); ok {
		if t, ok := newFees["t"].(float64); ok {
			if t < 0 {
				return -t
			}
			return t
		}
		if t, ok := newFees["t"].(string); ok {
			f, _ := strconv.ParseFloat(t, 64)
			if f < 0 {
				return -f
			}
			return f
		}
	}
	// 否则取任意币种对象的 totalFee（如 BGB.totalFee）
	for _, v := range raw {
		obj, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if tf, ok := obj["totalFee"].(float64); ok {
			if tf < 0 {
				return -tf
			}
			return tf
		}
		if tf, ok := obj["totalFee"].(string); ok {
			f, _ := strconv.ParseFloat(tf, 64)
			if f < 0 {
				return -f
			}
			return f
		}
	}
	return 0
}

// parseSpotOrderDetailResponse 解析现货订单查询响应
func (b *bitgetExchange) parseSpotOrderDetailResponse(responseBody string) (*model.Order, error) {
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			OrderId      string `json:"orderId"`
			Symbol       string `json:"symbol"`
			Side         string `json:"side"`
			OrderType    string `json:"orderType"`
			Price        string `json:"price"`
			Size         string `json:"size"`
			Status       string `json:"status"`
			FillPrice    string `json:"fillPrice"`    // 旧版
			FillQuantity string `json:"fillQuantity"` // 旧版
			PriceAvg     string `json:"priceAvg"`     // V2 成交均价
			BaseVolume   string `json:"baseVolume"`   // V2 成交数量( base )
			QuoteVolume  string `json:"quoteVolume"`
			Fee          string `json:"fee"`
			FeeDetail    string `json:"feeDetail"` // V2 手续费详情 JSON 字符串
			CTime        string `json:"cTime"`
			UTime        string `json:"uTime"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse spot order detail failed: %w", err)
	}

	if resp.Code != "00000" {
		return nil, fmt.Errorf("bitget API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("spot order not found")
	}

	item := resp.Data[0]

	price, _ := strconv.ParseFloat(item.Price, 64)
	quantity, _ := strconv.ParseFloat(item.Size, 64)
	// V2 使用 priceAvg / baseVolume；旧版使用 fillPrice / fillQuantity
	filledPriceStr := item.PriceAvg
	if filledPriceStr == "" {
		filledPriceStr = item.FillPrice
	}
	filledQtyStr := item.BaseVolume
	if filledQtyStr == "" {
		filledQtyStr = item.FillQuantity
	}
	fillPrice, _ := strconv.ParseFloat(filledPriceStr, 64)
	filledQty, _ := strconv.ParseFloat(filledQtyStr, 64)
	fee, _ := strconv.ParseFloat(item.Fee, 64)
	if fee == 0 && item.FeeDetail != "" {
		fee = parseSpotFeeDetail(item.FeeDetail)
	}
	if fee < 0 {
		fee = -fee
	}

	var orderSide model.OrderSide
	if item.Side == "buy" {
		orderSide = model.OrderSideBuy
	} else {
		orderSide = model.OrderSideSell
	}

	var orderType model.OrderType
	if item.OrderType == "market" {
		orderType = model.OrderTypeMarket
	} else {
		orderType = model.OrderTypeLimit
	}

	var orderStatus model.OrderStatus
	switch item.Status {
	case "new", "init", "live":
		orderStatus = model.OrderStatusNew
	case "partial-fill", "partial_fill", "partially_filled":
		orderStatus = model.OrderStatusPartiallyFilled
	case "full-fill", "full_fill", "filled":
		orderStatus = model.OrderStatusFilled
	case "cancelled", "canceled":
		orderStatus = model.OrderStatusCanceled
	default:
		orderStatus = model.OrderStatusNew
	}

	createTime, _ := strconv.ParseInt(item.CTime, 10, 64)
	updateTime, _ := strconv.ParseInt(item.UTime, 10, 64)

	return &model.Order{
		OrderID:     item.OrderId,
		Symbol:      item.Symbol,
		Side:        orderSide,
		Type:        orderType,
		Status:      orderStatus,
		Quantity:    quantity,
		Price:       price,
		FilledQty:   filledQty,
		FilledPrice: fillPrice,
		Fee:         fee,
		CreateTime:  time.Unix(createTime/1000, 0),
		UpdateTime:  time.Unix(updateTime/1000, 0),
	}, nil
}

// parseSpotOrderResponse 解析现货下单响应
func (b *bitgetExchange) parseSpotOrderResponse(responseBody string) (*model.Order, error) {
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			OrderId         string `json:"orderId"`
			ClientOid       string `json:"clientOid"`
			Symbol          string `json:"symbol"`
			Side            string `json:"side"`
			OrderType       string `json:"orderType"`
			Price           string `json:"price"`
			Size            string `json:"size"`
			Status          string `json:"status"`
			FillPrice       string `json:"fillPrice"`
			FillQuantity    string `json:"fillQuantity"`
			FillTotalAmount string `json:"fillTotalAmount"`
			CTime           string `json:"cTime"`
			UTime           string `json:"uTime"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse order response failed: %w", err)
	}

	if resp.Code != "00000" {
		return nil, fmt.Errorf("bitget API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	price, _ := strconv.ParseFloat(resp.Data.Price, 64)
	fillPrice, _ := strconv.ParseFloat(resp.Data.FillPrice, 64)
	quantity, _ := strconv.ParseFloat(resp.Data.Size, 64)
	filledQty, _ := strconv.ParseFloat(resp.Data.FillQuantity, 64)

	var orderSide model.OrderSide
	if resp.Data.Side == "buy" {
		orderSide = model.OrderSideBuy
	} else {
		orderSide = model.OrderSideSell
	}

	var orderType model.OrderType
	if resp.Data.OrderType == "market" {
		orderType = model.OrderTypeMarket
	} else {
		orderType = model.OrderTypeLimit
	}

	var orderStatus model.OrderStatus
	switch resp.Data.Status {
	case "new", "init":
		orderStatus = model.OrderStatusNew
	case "partial-fill":
		orderStatus = model.OrderStatusPartiallyFilled
	case "full-fill":
		orderStatus = model.OrderStatusFilled
	case "cancelled":
		orderStatus = model.OrderStatusCanceled
	default:
		orderStatus = model.OrderStatusNew
	}

	createTime, _ := strconv.ParseInt(resp.Data.CTime, 10, 64)
	updateTime, _ := strconv.ParseInt(resp.Data.UTime, 10, 64)

	return &model.Order{
		OrderID:     resp.Data.OrderId,
		Symbol:      resp.Data.Symbol,
		Side:        orderSide,
		Type:        orderType,
		Status:      orderStatus,
		Quantity:    quantity,
		Price:       price,
		FilledQty:   filledQty,
		FilledPrice: fillPrice,
		Fee:         0,
		CreateTime:  time.Unix(createTime/1000, 0),
		UpdateTime:  time.Unix(updateTime/1000, 0),
	}, nil
}

// parseFuturesOrderResponse 解析合约下单响应
func (b *bitgetExchange) parseFuturesOrderResponse(responseBody string) (*model.Order, error) {
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			OrderId   string `json:"orderId"`
			ClientOid string `json:"clientOid"`
			Symbol    string `json:"symbol"`
			Side      string `json:"side"`
			OrderType string `json:"orderType"`
			Price     string `json:"price"`
			Size      string `json:"size"`
			Status    string `json:"status"`
			FillPrice string `json:"fillPrice"`
			FillSize  string `json:"fillSize"`
			CTime     string `json:"cTime"`
			UTime     string `json:"uTime"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse order response failed: %w", err)
	}

	if resp.Code != "00000" {
		return nil, fmt.Errorf("bitget API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	price, _ := strconv.ParseFloat(resp.Data.Price, 64)
	fillPrice, _ := strconv.ParseFloat(resp.Data.FillPrice, 64)
	quantity, _ := strconv.ParseFloat(resp.Data.Size, 64)
	filledQty, _ := strconv.ParseFloat(resp.Data.FillSize, 64)

	var orderSide model.OrderSide
	if resp.Data.Side == "open_long" || resp.Data.Side == "close_short" {
		orderSide = model.OrderSideBuy
	} else {
		orderSide = model.OrderSideSell
	}

	var orderType model.OrderType
	if resp.Data.OrderType == "market" {
		orderType = model.OrderTypeMarket
	} else {
		orderType = model.OrderTypeLimit
	}

	var orderStatus model.OrderStatus
	switch resp.Data.Status {
	case "new", "init":
		orderStatus = model.OrderStatusNew
	case "partial-fill":
		orderStatus = model.OrderStatusPartiallyFilled
	case "full-fill":
		orderStatus = model.OrderStatusFilled
	case "cancelled":
		orderStatus = model.OrderStatusCanceled
	default:
		orderStatus = model.OrderStatusNew
	}

	createTime, _ := strconv.ParseInt(resp.Data.CTime, 10, 64)
	updateTime, _ := strconv.ParseInt(resp.Data.UTime, 10, 64)

	return &model.Order{
		OrderID:     resp.Data.OrderId,
		Symbol:      resp.Data.Symbol,
		Side:        orderSide,
		Type:        orderType,
		Status:      orderStatus,
		Quantity:    quantity,
		Price:       price,
		FilledQty:   filledQty,
		FilledPrice: fillPrice,
		Fee:         0,
		CreateTime:  time.Unix(createTime/1000, 0),
		UpdateTime:  time.Unix(updateTime/1000, 0),
	}, nil
}
