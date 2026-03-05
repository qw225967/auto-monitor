package okx

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/exchange"
	"github.com/qw225967/auto-monitor/internal/model"
)

// PlaceOrder 统一下单入口，按 MarketType 走现货或合约
func (o *okx) PlaceOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.isInitialized {
		return nil, exchange.ErrNotInitialized
	}
	switch req.MarketType {
	case model.MarketTypeSpot:
		return o.placeSpotOrder(req)
	case model.MarketTypeFutures:
		return o.placeFuturesOrder(req)
	default:
		return o.placeFuturesOrder(req)
	}
}

// placeSpotOrder 现货下单
func (o *okx) placeSpotOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	instId := ToOKXSpotInstId(req.Symbol)
	body := o.buildOrderBody(instId, "cash", req)
	return o.doPlaceOrder(body, req)
}

// placeFuturesOrder 合约下单（永续，全仓 cross）
func (o *okx) placeFuturesOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	instId := ToOKXSwapInstId(req.Symbol)
	body := o.buildOrderBody(instId, "cross", req)
	if req.ReduceOnly {
		if body["reduceOnly"] == nil {
			body["reduceOnly"] = true
		}
	}
	return o.doPlaceOrder(body, req)
}

// buildOrderBody 构建 POST /api/v5/trade/order 请求体
func (o *okx) buildOrderBody(instId, tdMode string, req *model.PlaceOrderRequest) map[string]interface{} {
	side := "buy"
	if req.Side == model.OrderSideSell {
		side = "sell"
	}
	ordType := "market"
	if req.Type == model.OrderTypeLimit {
		ordType = "limit"
	}
	body := map[string]interface{}{
		"instId":  instId,
		"tdMode":  tdMode,
		"side":    side,
		"ordType": ordType,
		"sz":      FormatQuantity(req.Quantity),
	}
	if ordType == "limit" && req.Price > 0 {
		body["px"] = FormatPrice(req.Price)
	}
	return body
}

// doPlaceOrder 发送下单请求并解析响应
func (o *okx) doPlaceOrder(body map[string]interface{}, req *model.PlaceOrderRequest) (*model.Order, error) {
	apiKey, secretKey, passphrase := o.getAPIKeys()
	requestPath := constants.OkexPathTradeOrder
	bodyBytes, _ := json.Marshal(body)
	bodyStr := string(bodyBytes)

	// #region agent log
	{
		instID, _ := body["instId"].(string)
		tdMode, _ := body["tdMode"].(string)
		side, _ := body["side"].(string)
		ordType, _ := body["ordType"].(string)
		sz, _ := body["sz"].(string)
		debugLogOKX(
			"internal/exchange/okx/order.go:doPlaceOrder:req",
			"okx place order request",
			map[string]interface{}{
				"symbol": req.Symbol,
				"marketType": string(req.MarketType),
				"reduceOnly": req.ReduceOnly,
				"instId": instID,
				"tdMode": tdMode,
				"side": side,
				"ordType": ordType,
				"sz": sz,
				"rawBody": bodyStr,
			},
			"H_okx_order_req",
			"pre",
		)
	}
	// #endregion

	timestamp := GetOKXTimestamp()
	sign := BuildOKXSign(timestamp, "POST", requestPath, bodyStr, secretKey)
	headers := BuildOKXHeaders(apiKey, sign, timestamp, passphrase)

	apiURL := constants.OkexBaseUrl + requestPath

	o.mu.RLock()
	restClient := o.restClient
	o.mu.RUnlock()

	responseBody, err := restClient.DoPostWithHeaders(apiURL, bodyStr, headers)
	if err != nil {
		// #region agent log
		debugLogOKX(
			"internal/exchange/okx/order.go:doPlaceOrder:netErr",
			"okx place order network/client error",
			map[string]interface{}{
				"symbol": req.Symbol,
				"marketType": string(req.MarketType),
				"error": err.Error(),
			},
			"H_okx_order_net",
			"pre",
		)
		// #endregion
		return nil, fmt.Errorf("okx place order: %w", err)
	}

	// #region agent log
	{
		code, msg, sCode, sMsg := extractOKXErrorDetails(responseBody)
		rb := responseBody
		if len(rb) > 2000 {
			rb = rb[:2000] + "...(truncated)"
		}
		// 规避极端情况下返回里包含敏感字段（理论上 OKX 不会把 key 回显，但做一层过滤）
		rb = strings.ReplaceAll(rb, apiKey, "***")
		rb = strings.ReplaceAll(rb, passphrase, "***")
		debugLogOKX(
			"internal/exchange/okx/order.go:doPlaceOrder:resp",
			"okx place order response",
			map[string]interface{}{
				"symbol": req.Symbol,
				"marketType": string(req.MarketType),
				"code": code,
				"msg": msg,
				"sCode": sCode,
				"sMsg": sMsg,
				"rawResponse": rb,
			},
			"H_okx_order_resp",
			"pre",
		)
	}
	// #endregion

	return o.parseOrderResponse(responseBody, req)
}

// okxOrderData 下单/订单详情返回的单条 data
type okxOrderData struct {
	OrdId     string `json:"ordId"`
	InstId    string `json:"instId"`
	Side      string `json:"side"`
	OrdType   string `json:"ordType"`
	State     string `json:"state"`
	Px        string `json:"px"`
	AvgPx     string `json:"avgPx"`
	Sz        string `json:"sz"`
	AccFillSz string `json:"accFillSz"`
	Fee       string `json:"fee"`
	CTime     string `json:"cTime"`
	UTime     string `json:"uTime"`
}

// parseOrderResponse 解析 OKX 下单响应
func (o *okx) parseOrderResponse(responseBody string, req *model.PlaceOrderRequest) (*model.Order, error) {
	if err := CheckOKXAPIError(responseBody); err != nil {
		return nil, err
	}
	var resp struct {
		Code string         `json:"code"`
		Data []okxOrderData `json:"data"`
	}
	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse order response: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("okx order: no data in response")
	}
	d := &resp.Data[0]
	price, _ := strconv.ParseFloat(d.Px, 64)
	avgPrice, _ := strconv.ParseFloat(d.AvgPx, 64)
	quantity, _ := strconv.ParseFloat(d.Sz, 64)
	filledQty, _ := strconv.ParseFloat(d.AccFillSz, 64)
	fee, _ := strconv.ParseFloat(d.Fee, 64)

	var orderSide model.OrderSide
	if d.Side == "buy" {
		orderSide = model.OrderSideBuy
	} else {
		orderSide = model.OrderSideSell
	}
	var orderType model.OrderType
	if d.OrdType == "market" {
		orderType = model.OrderTypeMarket
	} else {
		orderType = model.OrderTypeLimit
	}
	status := okxStateToOrderStatus(d.State)

	createTime := time.Now()
	if d.CTime != "" {
		if ms, err := strconv.ParseInt(d.CTime, 10, 64); err == nil {
			createTime = time.UnixMilli(ms)
		}
	}
	updateTime := createTime
	if d.UTime != "" {
		if ms, err := strconv.ParseInt(d.UTime, 10, 64); err == nil {
			updateTime = time.UnixMilli(ms)
		}
	}

	symbol := req.Symbol
	if symbol == "" {
		symbol = FromOKXInstId(d.InstId)
	}
	return &model.Order{
		OrderID:     d.OrdId,
		Symbol:      symbol,
		Side:        orderSide,
		Type:        orderType,
		Status:      status,
		Quantity:    quantity,
		Price:       price,
		FilledQty:   filledQty,
		FilledPrice: avgPrice,
		Fee:         fee,
		CreateTime:  createTime,
		UpdateTime:  updateTime,
	}, nil
}

// QueryFuturesOrder 查询合约订单详情（GET /api/v5/trade/order?instId=X&ordId=Y）
func (o *okx) QueryFuturesOrder(symbol, orderID string) (*model.Order, error) {
	instId := ToOKXSwapInstId(symbol)
	return o.queryOrder(instId, orderID, symbol)
}

// QuerySpotOrder 查询现货订单详情
func (o *okx) QuerySpotOrder(symbol, orderID string) (*model.Order, error) {
	instId := ToOKXSpotInstId(symbol)
	return o.queryOrder(instId, orderID, symbol)
}

// queryOrder 统一订单查询
func (o *okx) queryOrder(instId, orderID, symbol string) (*model.Order, error) {
	apiKey, secretKey, passphrase := o.getAPIKeys()
	requestPath := constants.OkexPathTradeOrder + "?instId=" + instId + "&ordId=" + orderID
	timestamp := GetOKXTimestamp()
	sign := BuildOKXSign(timestamp, "GET", requestPath, "", secretKey)
	headers := BuildOKXHeaders(apiKey, sign, timestamp, passphrase)

	apiURL := constants.OkexBaseUrl + requestPath

	o.mu.RLock()
	restClient := o.restClient
	o.mu.RUnlock()

	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("okx query order: %w", err)
	}

	req := &model.PlaceOrderRequest{Symbol: symbol}
	return o.parseOrderResponse(responseBody, req)
}

func okxStateToOrderStatus(state string) model.OrderStatus {
	switch state {
	case "live":
		return model.OrderStatusNew
	case "partially_filled":
		return model.OrderStatusPartiallyFilled
	case "filled":
		return model.OrderStatusFilled
	case "canceled":
		return model.OrderStatusCanceled
	case "rejected":
		return model.OrderStatusRejected
	case "expired":
		return model.OrderStatusExpired
	default:
		return model.OrderStatusNew
	}
}
