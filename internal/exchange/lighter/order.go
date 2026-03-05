package lighter

import (
	"encoding/json"
	"fmt"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/model"
)

// placeOrderInternal 内部下单方法
func (l *lighter) placeOrderInternal(req *model.PlaceOrderRequest) (*model.Order, error) {
	nonce := l.getNextNonce()
	headers := buildAuthHeaders(l.apiKey, l.token, nonce)

	// 标准化符号
	symbol := normalizeLighterSymbol(req.Symbol)

	// 生成唯一的 client_order_index（使用时间戳+随机数确保唯一性）
	// 文档要求：client_order_index 是唯一标识符，用于后续取消订单
	clientOrderIndex := time.Now().UnixNano() % 1000000000 // 使用纳秒时间戳的后9位

	// 构建订单请求
	orderReq := map[string]interface{}{
		"symbol":           symbol,
		"side":             string(req.Side), // "BUY" or "SELL"
		"type":             string(req.Type), // "LIMIT" or "MARKET"
		"quantity":         formatQuantity(req.Quantity),
		"nonce":            nonce,
		"client_order_index": clientOrderIndex,
	}

	// 如果是限价单，添加价格和 time_in_force
	if req.Type == model.OrderTypeLimit {
		orderReq["price"] = formatPrice(req.Price)
		// time_in_force: 文档要求提供
		// 默认使用 GOOD_TILL_TIME（类似 GTC）
		orderReq["time_in_force"] = "GOOD_TILL_TIME"
	}

	// 如果是合约，添加减仓标志
	if req.MarketType == model.MarketTypeFutures && req.ReduceOnly {
		orderReq["reduce_only"] = true
	}

	bodyJSON, _ := json.Marshal(orderReq)
	apiURL := fmt.Sprintf("%s%s", constants.LighterRestBaseUrl, constants.LighterOrderPath)

	responseBody, err := l.restClient.DoPostWithHeaders(apiURL, string(bodyJSON), headers)
	if err != nil {
		return nil, fmt.Errorf("failed to place order: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		OrderID   string `json:"order_id"`
		Symbol    string `json:"symbol"`
		Side      string `json:"side"`
		Type      string `json:"type"`
		Quantity  string `json:"quantity"`
		Price     string `json:"price"`
		Status    string `json:"status"`
		FilledQty string `json:"filled_qty"`
		CreatedAt int64  `json:"created_at"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse order response: %w", err)
	}

	// 转换为 model.Order
	qty, _ := parseFloat(resp.Quantity)
	price, _ := parseFloat(resp.Price)
	filledQty, _ := parseFloat(resp.FilledQty)

	order := &model.Order{
		OrderID:     resp.OrderID,
		Symbol:      denormalizeSymbol(resp.Symbol),
		Side:        model.OrderSide(resp.Side),
		Type:        model.OrderType(resp.Type),
		Status:      parseOrderStatus(resp.Status),
		Quantity:    qty,
		Price:       price,
		FilledQty:   filledQty,
		FilledPrice: 0, // 需要计算
		Fee:         0, // 需要从其他接口获取
		CreateTime:  time.Unix(resp.CreatedAt/1000, 0),
		UpdateTime:  time.Now(),
	}

	return order, nil
}

// parseOrderStatus 解析订单状态
func parseOrderStatus(status string) model.OrderStatus {
	switch status {
	case "NEW", "PENDING":
		return model.OrderStatusNew
	case "FILLED":
		return model.OrderStatusFilled
	case "PARTIALLY_FILLED":
		return model.OrderStatusPartiallyFilled
	case "CANCELLED", "CANCELED":
		return model.OrderStatusCanceled
	case "REJECTED":
		return model.OrderStatusRejected
	case "EXPIRED":
		return model.OrderStatusExpired
	default:
		return model.OrderStatusNew // 默认返回 New 状态
	}
}
