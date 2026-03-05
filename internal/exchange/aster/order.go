package aster

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/model"
)

// placeOrderInternal 内部下单方法
func (a *aster) placeOrderInternal(req *model.PlaceOrderRequest) (*model.Order, error) {
	apiKey, secretKey := a.getAPIKeys()
	
	// 构建订单请求参数
	timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
	params := fmt.Sprintf("symbol=%s&side=%s&type=%s&quantity=%s&timestamp=%s",
		req.Symbol,
		string(req.Side),
		string(req.Type),
		formatQuantity(req.Quantity),
		timestamp)
	
	// 如果是限价单，添加价格和 timeInForce
	if req.Type == model.OrderTypeLimit {
		params += fmt.Sprintf("&price=%s", formatPrice(req.Price))
		// 限价单必须提供 timeInForce，默认使用 GTC (Good Till Canceled)
		timeInForce := "GTC"
		params += fmt.Sprintf("&timeInForce=%s", timeInForce)
	}
	
	// 合约下单需要额外参数
	if req.MarketType == model.MarketTypeFutures {
		// positionSide: 持仓方向（BOTH, LONG, SHORT），默认 BOTH
		// 如果请求中指定了 PositionSide，使用它；否则使用 BOTH
		positionSide := "BOTH"
		if req.PositionSide != "" {
			// 将 model.PositionSide (LONG/SHORT) 转换为 API 格式
			if req.PositionSide == model.PositionSideLong {
				positionSide = "LONG"
			} else if req.PositionSide == model.PositionSideShort {
				positionSide = "SHORT"
			}
		}
		params += fmt.Sprintf("&positionSide=%s", positionSide)
		
		// reduceOnly: 是否只减仓，默认 false
		if req.ReduceOnly {
			params += fmt.Sprintf("&reduceOnly=true")
		}
	}
	
	// 签名
	signature := signRequest(params, secretKey)
	params += fmt.Sprintf("&signature=%s", signature)
	
	// 根据市场类型选择API Base URL和路径
	var baseURL, apiPath string
	if req.MarketType == model.MarketTypeFutures {
		baseURL = constants.AsterFuturesRestBaseUrl
		apiPath = constants.AsterFuturesOrderPath
	} else {
		baseURL = constants.AsterSpotRestBaseUrl
		apiPath = constants.AsterSpotOrderPath
	}
	
	apiURL := fmt.Sprintf("%s%s", baseURL, apiPath)
	
	headers := make(map[string]string)
	headers["X-MBX-APIKEY"] = apiKey
	headers["Content-Type"] = "application/x-www-form-urlencoded"
	
	responseBody, err := a.restClient.DoPostWithHeaders(apiURL, params, headers)
	if err != nil {
		return nil, fmt.Errorf("failed to place order: %w", err)
	}
	
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}
	
	var resp struct {
		OrderID       string `json:"orderId"`
		Symbol        string `json:"symbol"`
		Side          string `json:"side"`
		Type          string `json:"type"`
		OrigQty       string `json:"origQty"`
		Price         string `json:"price"`
		Status        string `json:"status"`
		ExecutedQty   string `json:"executedQty"`
		TransactTime  int64  `json:"transactTime"`
	}
	
	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse order response: %w", err)
	}
	
	// 转换为 model.Order
	qty, _ := parseFloat(resp.OrigQty)
	price, _ := parseFloat(resp.Price)
	filledQty, _ := parseFloat(resp.ExecutedQty)
	
	order := &model.Order{
		OrderID:     resp.OrderID,
		Symbol:      resp.Symbol,
		Side:        model.OrderSide(resp.Side),
		Type:        model.OrderType(resp.Type),
		Status:      parseOrderStatus(resp.Status),
		Quantity:    qty,
		Price:       price,
		FilledQty:   filledQty,
		FilledPrice: 0,
		Fee:         0,
		CreateTime:  time.Unix(resp.TransactTime/1000, 0),
		UpdateTime:  time.Now(),
	}
	
	return order, nil
}

// QueryFuturesOrder 查询合约订单详情（GET /fapi/v1/order，Binance 风格）
func (a *aster) QueryFuturesOrder(symbol, orderID string) (*model.Order, error) {
	apiKey, secretKey := a.getAPIKeys()
	timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
	params := fmt.Sprintf("symbol=%s&orderId=%s&timestamp=%s", symbol, orderID, timestamp)
	signature := signRequest(params, secretKey)
	params += "&signature=" + signature

	apiURL := fmt.Sprintf("%s%s?%s", constants.AsterFuturesRestBaseUrl, constants.AsterFuturesOrderPath, params)
	headers := map[string]string{"X-MBX-APIKEY": apiKey}

	responseBody, err := a.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("query futures order failed: %w", err)
	}
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}
	return a.parseOrderQueryResponse(responseBody)
}

// QuerySpotOrder 查询现货订单详情（GET /api/v1/order，Binance 风格）
func (a *aster) QuerySpotOrder(symbol, orderID string) (*model.Order, error) {
	apiKey, secretKey := a.getAPIKeys()
	timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
	params := fmt.Sprintf("symbol=%s&orderId=%s&timestamp=%s", symbol, orderID, timestamp)
	signature := signRequest(params, secretKey)
	params += "&signature=" + signature

	apiURL := fmt.Sprintf("%s%s?%s", constants.AsterSpotRestBaseUrl, constants.AsterSpotOrderPath, params)
	headers := map[string]string{"X-MBX-APIKEY": apiKey}

	responseBody, err := a.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("query spot order failed: %w", err)
	}
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}
	return a.parseOrderQueryResponse(responseBody)
}

// parseOrderQueryResponse 解析查单响应（与下单响应结构兼容，含可选 avgPrice/updateTime）
func (a *aster) parseOrderQueryResponse(responseBody string) (*model.Order, error) {
	var resp struct {
		OrderID      string `json:"orderId"`
		Symbol       string `json:"symbol"`
		Side         string `json:"side"`
		Type         string `json:"type"`
		OrigQty      string `json:"origQty"`
		Price        string `json:"price"`
		Status       string `json:"status"`
		ExecutedQty  string `json:"executedQty"`
		TransactTime int64  `json:"transactTime"`
		UpdateTime   int64  `json:"updateTime"`
		AvgPrice     string `json:"avgPrice"`
	}
	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse order query response failed: %w", err)
	}

	qty, _ := parseFloat(resp.OrigQty)
	price, _ := parseFloat(resp.Price)
	filledQty, _ := parseFloat(resp.ExecutedQty)
	avgPrice, _ := parseFloat(resp.AvgPrice)
	if avgPrice == 0 {
		avgPrice = price
	}

	updateTime := time.Now()
	if resp.UpdateTime > 0 {
		updateTime = time.Unix(resp.UpdateTime/1000, 0)
	}

	return &model.Order{
		OrderID:     resp.OrderID,
		Symbol:      resp.Symbol,
		Side:        model.OrderSide(resp.Side),
		Type:        model.OrderType(resp.Type),
		Status:      parseOrderStatus(resp.Status),
		Quantity:    qty,
		Price:       price,
		FilledQty:   filledQty,
		FilledPrice: avgPrice,
		Fee:         0,
		CreateTime:  time.Unix(resp.TransactTime/1000, 0),
		UpdateTime:  updateTime,
	}, nil
}

// parseOrderStatus 解析订单状态
func parseOrderStatus(status string) model.OrderStatus {
	switch status {
	case "NEW":
		return model.OrderStatusNew
	case "FILLED":
		return model.OrderStatusFilled
	case "PARTIALLY_FILLED":
		return model.OrderStatusPartiallyFilled
	case "CANCELED":
		return model.OrderStatusCanceled
	case "REJECTED":
		return model.OrderStatusRejected
	case "EXPIRED":
		return model.OrderStatusExpired
	default:
		return model.OrderStatusNew
	}
}
