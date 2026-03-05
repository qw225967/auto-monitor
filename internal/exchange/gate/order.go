package gate

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/model"
)

// placeSpotOrder 现货下单
func (g *gateExchange) placeSpotOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	g.mu.RLock()
	restClient := g.restClient
	g.mu.RUnlock()
	
	apiKey, secretKey := g.getAPIKeys()

	side := "buy"
	if req.Side == model.OrderSideSell {
		side = "sell"
	}

	orderType := "market"
	if req.Type == model.OrderTypeLimit {
		if req.Price <= 0 {
			return nil, fmt.Errorf("limit order requires price")
		}
		orderType = "limit"
	}

	// 构建请求体
	orderReq := map[string]interface{}{
		"currency_pair": normalizeGateSymbol(req.Symbol),
		"side":          side,
		"type":          orderType,
		"amount":        formatQuantity(req.Quantity),
	}

	if orderType == "limit" {
		orderReq["price"] = formatPrice(req.Price)
		orderReq["time_in_force"] = "gtc"
	}

	requestBody, _ := json.Marshal(orderReq)
	timestamp := getCurrentTimestamp()
	
	// 生成签名
	signature := signRequest("POST", constants.GateSpotOrderPath, "", string(requestBody), secretKey, timestamp)
	
	// 发送 HTTP POST 请求
	apiURL := fmt.Sprintf("%s%s", constants.GateRestBaseUrl, constants.GateSpotOrderPath)
	headers := buildHeaders(apiKey, signature, timestamp)

	responseBody, err := restClient.DoPostWithHeaders(apiURL, string(requestBody), headers)
	if err != nil {
		return nil, fmt.Errorf("place spot order failed: %w", err)
	}

	// 解析响应
	return g.parseSpotOrderResponse(responseBody)
}

// placeFuturesOrder 合约下单
func (g *gateExchange) placeFuturesOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	g.mu.RLock()
	restClient := g.restClient
	gateSymbol := normalizeGateSymbol(req.Symbol)
	mult := g.quantoMultipliers[gateSymbol]
	g.mu.RUnlock()
	
	apiKey, secretKey := g.getAPIKeys()

	if mult <= 0 {
		mult = 1.0
	}
	// Gate.io 合约 size = 合约张数，张数 = 币数量 / quanto_multiplier
	sizeLots := int64(math.Round(req.Quantity / mult))
	if req.Side == model.OrderSideSell {
		sizeLots = -sizeLots
	}

	orderType := "market"
	if req.Type == model.OrderTypeLimit {
		orderType = "limit"
	}

	// 构建请求体
	orderReq := map[string]interface{}{
		"contract": gateSymbol,
		"size":     strconv.FormatInt(sizeLots, 10),
	}

	if orderType == "limit" {
		// 限价单：需要价格，tif 可以是 gtc/ioc/poc
		orderReq["price"] = formatPrice(req.Price)
		orderReq["tif"] = "gtc"
	} else {
		// 市价单：price 必须为 "0"，tif 必须是 "ioc"
		orderReq["price"] = "0"
		orderReq["tif"] = "ioc"
	}

	if req.ReduceOnly {
		orderReq["reduce_only"] = true
	}

	requestBody, _ := json.Marshal(orderReq)
	timestamp := getCurrentTimestamp()
	
	// 生成签名
	signature := signRequest("POST", constants.GateFuturesOrderPath, "", string(requestBody), secretKey, timestamp)
	
	// 发送 HTTP POST 请求
	apiURL := fmt.Sprintf("%s%s", constants.GateRestBaseUrl, constants.GateFuturesOrderPath)
	headers := buildHeaders(apiKey, signature, timestamp)

	responseBody, err := restClient.DoPostWithHeaders(apiURL, string(requestBody), headers)
	if err != nil {
		return nil, fmt.Errorf("place futures order failed: %w", err)
	}

	// 解析响应
	return g.parseFuturesOrderResponse(responseBody)
}

// parseSpotOrderResponse 解析现货下单响应
func (g *gateExchange) parseSpotOrderResponse(responseBody string) (*model.Order, error) {
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var orderResp struct {
		Id            string `json:"id"`
		Text          string `json:"text"`
		CreateTime    string `json:"create_time"`
		UpdateTime    string `json:"update_time"`
		CurrencyPair  string `json:"currency_pair"`
		Status        string `json:"status"`
		Type          string `json:"type"`
		Account       string `json:"account"`
		Side          string `json:"side"`
		Amount        string `json:"amount"`
		Price         string `json:"price"`
		FilledAmount  string `json:"filled_amount"`
		FilledTotal   string `json:"filled_total"`
		AvgDealPrice  string `json:"avg_deal_price"`
		Fee           string `json:"fee"`
		FeeCurrency   string `json:"fee_currency"`
	}

	if err := json.Unmarshal([]byte(responseBody), &orderResp); err != nil {
		return nil, fmt.Errorf("parse order response failed: %w", err)
	}

	price, _ := strconv.ParseFloat(orderResp.Price, 64)
	avgPrice, _ := strconv.ParseFloat(orderResp.AvgDealPrice, 64)
	quantity, _ := strconv.ParseFloat(orderResp.Amount, 64)
	filledQty, _ := strconv.ParseFloat(orderResp.FilledAmount, 64)
	fee, _ := strconv.ParseFloat(orderResp.Fee, 64)

	var orderSide model.OrderSide
	if orderResp.Side == "buy" {
		orderSide = model.OrderSideBuy
	} else {
		orderSide = model.OrderSideSell
	}

	var orderType model.OrderType
	if orderResp.Type == "market" {
		orderType = model.OrderTypeMarket
	} else {
		orderType = model.OrderTypeLimit
	}

	var orderStatus model.OrderStatus
	switch orderResp.Status {
	case "open":
		orderStatus = model.OrderStatusNew
	case "closed":
		orderStatus = model.OrderStatusFilled
	case "cancelled":
		orderStatus = model.OrderStatusCanceled
	default:
		orderStatus = model.OrderStatusNew
	}

	createTime, _ := strconv.ParseInt(orderResp.CreateTime, 10, 64)
	updateTime, _ := strconv.ParseInt(orderResp.UpdateTime, 10, 64)

	return &model.Order{
		OrderID:     orderResp.Id,
		Symbol:      orderResp.CurrencyPair,
		Side:        orderSide,
		Type:        orderType,
		Status:      orderStatus,
		Quantity:    quantity,
		Price:       price,
		FilledQty:   filledQty,
		FilledPrice: avgPrice,
		Fee:         fee,
		CreateTime:  time.Unix(createTime, 0),
		UpdateTime:  time.Unix(updateTime, 0),
	}, nil
}

// parseFeeFromGateFuturesOrder 从合约订单响应中解析手续费（优先 struct 的 fee，否则从 raw 中尝试 order_fee 等键）
func parseFeeFromGateFuturesOrder(responseBody, feeStr string) float64 {
	if feeStr != "" {
		fee, _ := strconv.ParseFloat(feeStr, 64)
		if fee < 0 {
			fee = -fee
		}
		return fee
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(responseBody), &raw); err != nil {
		return 0
	}
	for _, key := range []string{"fee", "order_fee", "realized_fee", "realised_fee"} {
		if v, ok := raw[key]; ok && v != nil {
			switch val := v.(type) {
			case string:
				if f, err := strconv.ParseFloat(val, 64); err == nil {
					if f < 0 {
						f = -f
					}
					return f
				}
			case float64:
				if val < 0 {
					val = -val
				}
				return val
			}
		}
	}
	return 0
}

// parseFuturesOrderResponse 解析合约下单响应
func (g *gateExchange) parseFuturesOrderResponse(responseBody string) (*model.Order, error) {
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var orderResp struct {
		Id          int64   `json:"id"`
		User        int64   `json:"user"`
		CreateTime  float64 `json:"create_time"`
		FinishTime  float64 `json:"finish_time"`
		Contract    string  `json:"contract"`
		Size        int64   `json:"size"`
		Price       string  `json:"price"`
		FillPrice   string  `json:"fill_price"`
		Left        int64   `json:"left"`
		Status      string  `json:"status"`
		Fee  string `json:"fee"`
		Mkfr string `json:"mkfr"` // maker fee rate，用于下方无 fee 时估算
		Tkfr string `json:"tkfr"` // taker fee rate
	}

	if err := json.Unmarshal([]byte(responseBody), &orderResp); err != nil {
		return nil, fmt.Errorf("parse order response failed: %w", err)
	}

	price, _ := strconv.ParseFloat(orderResp.Price, 64)
	fillPrice, _ := strconv.ParseFloat(orderResp.FillPrice, 64)
	fee := parseFeeFromGateFuturesOrder(responseBody, orderResp.Fee)
	size := orderResp.Size
	if size < 0 {
		size = -size
	}
	quantity := float64(size)
	// Gate Left 与 Size 同号，已成交量 = |Size - Left|
	leftAbs := orderResp.Left
	if leftAbs < 0 {
		leftAbs = -leftAbs
	}
	filledQty := float64(size - leftAbs)
	if filledQty < 0 {
		filledQty = 0
	}

	var orderSide model.OrderSide
	if orderResp.Size > 0 {
		orderSide = model.OrderSideBuy
	} else {
		orderSide = model.OrderSideSell
	}

	var orderStatus model.OrderStatus
	switch orderResp.Status {
	case "open":
		orderStatus = model.OrderStatusNew
	case "finished":
		orderStatus = model.OrderStatusFilled
	case "cancelled":
		orderStatus = model.OrderStatusCanceled
	default:
		orderStatus = model.OrderStatusNew
	}

	// 将浮点数时间戳转换为 int64（秒级）
	createTime := int64(orderResp.CreateTime)
	finishTime := int64(orderResp.FinishTime)

	return &model.Order{
		OrderID:     strconv.FormatInt(orderResp.Id, 10),
		Symbol:      orderResp.Contract,
		Side:        orderSide,
		Type:        model.OrderTypeLimit,
		Status:      orderStatus,
		Quantity:    quantity,
		Price:       price,
		FilledQty:   filledQty,
		FilledPrice: fillPrice,
		Fee:         fee,
		CreateTime:  time.Unix(createTime, 0),
		UpdateTime:  time.Unix(finishTime, 0),
	}, nil
}

// QueryFuturesOrder 查询合约订单详情
func (g *gateExchange) QueryFuturesOrder(symbol, orderID string) (*model.Order, error) {
	g.mu.RLock()
	restClient := g.restClient
	gateSymbol := normalizeGateSymbol(symbol)
	mult := g.quantoMultipliers[gateSymbol]
	g.mu.RUnlock()

	apiKey, secretKey := g.getAPIKeys()

	path := constants.GateFuturesOrderDetailPath + orderID
	timestamp := getCurrentTimestamp()

	signature := signRequest("GET", path, "", "", secretKey, timestamp)

	apiURL := fmt.Sprintf("%s%s", constants.GateRestBaseUrl, path)
	headers := buildHeaders(apiKey, signature, timestamp)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("query futures order failed: %w", err)
	}

	order, err := g.parseFuturesOrderResponse(responseBody)
	if err != nil {
		return nil, err
	}

	// 返回的 Symbol 使用请求的 symbol，与调用方一致（Gate API 的 contract 可能为 ARC_USDT 等与请求 ASTER_USDT 不同）
	order.Symbol = symbol

	// Gate 合约 size 是张数，转换为币数量
	if mult > 0 {
		absSize := order.Quantity
		if absSize < 0 {
			absSize = -absSize
		}
		order.Quantity = absSize * mult

		absFilledQty := order.FilledQty
		if absFilledQty < 0 {
			absFilledQty = -absFilledQty
		}
		order.FilledQty = absFilledQty * mult
	} else {
		if order.Quantity < 0 {
			order.Quantity = -order.Quantity
		}
		if order.FilledQty < 0 {
			order.FilledQty = -order.FilledQty
		}
	}

	// 单笔订单接口不返回 fee，用 mkfr/tkfr 估算：手续费 ≈ 成交额(币数*成交价) * 费率，优先 tkfr
	if order.Fee == 0 && order.FilledQty > 0 && order.FilledPrice > 0 {
		var rateResp struct {
			Tkfr string `json:"tkfr"`
			Mkfr string `json:"mkfr"`
		}
		if _ = json.Unmarshal([]byte(responseBody), &rateResp); rateResp.Tkfr != "" || rateResp.Mkfr != "" {
			tkfr, _ := strconv.ParseFloat(rateResp.Tkfr, 64)
			mkfr, _ := strconv.ParseFloat(rateResp.Mkfr, 64)
			rate := tkfr
			if rate == 0 {
				rate = mkfr
			}
			order.Fee = order.FilledQty * order.FilledPrice * rate
			if order.Fee < 0 {
				order.Fee = -order.Fee
			}
		}
	}

	return order, nil
}

// QuerySpotOrder 查询现货订单详情
func (g *gateExchange) QuerySpotOrder(symbol, orderID string) (*model.Order, error) {
	g.mu.RLock()
	restClient := g.restClient
	g.mu.RUnlock()

	apiKey, secretKey := g.getAPIKeys()

	gateSymbol := normalizeGateSymbol(symbol)
	path := constants.GateSpotOrderDetailPath + orderID
	queryString := fmt.Sprintf("currency_pair=%s", gateSymbol)
	timestamp := getCurrentTimestamp()

	signature := signRequest("GET", path, queryString, "", secretKey, timestamp)

	apiURL := fmt.Sprintf("%s%s?%s", constants.GateRestBaseUrl, path, queryString)
	headers := buildHeaders(apiKey, signature, timestamp)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("query spot order failed: %w", err)
	}

	return g.parseSpotOrderResponse(responseBody)
}

// setLeverage 设置合约杠杆，在订阅合约 symbol 时调用
// 注意：由 SubscribeTicker 在已持 g.mu 时调用，此处不再加锁，避免死锁；仅读 restClient（API 密钥从全局配置读取）
func (g *gateExchange) setLeverage(symbol string, leverage int) error {
	restClient := g.restClient
	apiKey, secretKey := g.getAPIKeys()

	contract := normalizeGateSymbol(symbol)
	path := constants.GatePositionPath + "/" + contract + constants.GateFuturesLeveragePathSuffix
	// leverage=0 表示全仓；cross_leverage_limit 为全仓下的杠杆上限
	query := fmt.Sprintf("leverage=0&cross_leverage_limit=%d", leverage)

	timestamp := getCurrentTimestamp()
	signature := signRequest("POST", path, query, "", secretKey, timestamp)
	apiURL := fmt.Sprintf("%s%s?%s", constants.GateRestBaseUrl, path, query)
	headers := buildHeaders(apiKey, signature, timestamp)

	responseBody, err := restClient.DoPostWithHeaders(apiURL, "", headers)
	if err != nil {
		return fmt.Errorf("set leverage failed: %w", err)
	}
	return checkAPIError(responseBody)
}
