package hyperliquid

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/model"
)

// placeSpotOrder 现货下单
func (h *hyperliquidExchange) placeSpotOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	// Hyperliquid 的现货和合约使用相同的下单接口
	return h.placeFuturesOrder(req)
}

// placeFuturesOrder 合约下单
func (h *hyperliquidExchange) placeFuturesOrder(req *model.PlaceOrderRequest) (*model.Order, error) {
	walletAddress, privateKey := h.getWalletCredentials()
	
	// 确保地址是小写（关键！）
	walletAddress = strings.ToLower(walletAddress)
	
	// 标准化符号
	_ = normalizeSymbol(req.Symbol, true) // coin
	
	// 确定方向
	isBuy := req.Side == model.OrderSideBuy
	
	// 确定订单类型和价格
	var limitPx string
	
	if req.Type == model.OrderTypeMarket {
		// 市价单：使用极端价格实现
		if isBuy {
			limitPx = "999999" // 买入时使用极高价格
		} else {
			limitPx = "0.01" // 卖出时使用极低价格
		}
	} else {
		// 限价单
		limitPx = formatPrice(req.Price)
	}
	
	// 获取时间戳（毫秒）
	timestamp := getCurrentNonce()
	
	// 构建订单参数（字段顺序很重要！必须与 go-hyperliquid SDK 完全一致）
	// 参考：go-hyperliquid/actions.go OrderAction
	type OrderLimit struct {
		Tif string `msgpack:"tif" json:"tif"`
	}
	
	type OrderType struct {
		Limit OrderLimit `msgpack:"limit" json:"limit"`
	}
	
	type Order struct {
		A int        `msgpack:"a" json:"a"` // asset (1st)
		B bool       `msgpack:"b" json:"b"` // is_buy (2nd)
		P string     `msgpack:"p" json:"p"` // price (3rd)
		S string     `msgpack:"s" json:"s"` // size (4th)
		R bool       `msgpack:"r" json:"r"` // reduce_only (5th)
		T OrderType  `msgpack:"t" json:"t"` // order_type (6th)
	}
	
	// CRITICAL: OrderAction 字段顺序必须与 SDK 一致！
	// SDK 顺序: type -> orders -> grouping (NOT type -> grouping -> orders!)
	type OrderAction struct {
		Type     string  `msgpack:"type" json:"type"`         // 1st
		Orders   []Order `msgpack:"orders" json:"orders"`     // 2nd
		Grouping string  `msgpack:"grouping" json:"grouping"` // 3rd
	}
	
	// 构建订单类型
	var orderTypeStruct OrderType
	if req.Type == model.OrderTypeMarket {
		orderTypeStruct = OrderType{
			Limit: OrderLimit{Tif: "Ioc"},
		}
	} else {
		orderTypeStruct = OrderType{
			Limit: OrderLimit{Tif: "Gtc"},
		}
	}
	
	orderAction := OrderAction{
		Type:     constants.HyperliquidActionOrder,
		Grouping: "na",
		Orders: []Order{
			{
				A: 1,                             // 资产索引
				B: isBuy,                         // 买入/卖出
				P: limitPx,                       // 限价
				S: formatQuantity(req.Quantity),  // 数量
				R: req.ReduceOnly,                // 只减仓
				T: orderTypeStruct,               // 订单类型
			},
		},
	}
	
	// 签名 (使用 EIP-712 和 Msgpack)
	signatureObj, err := signOrderWithEIP712(privateKey, orderAction, timestamp)
	if err != nil {
		return nil, fmt.Errorf("sign order failed: %w", err)
	}
	
	// 构建请求
	request := buildExchangeRequestWithEIP712(constants.HyperliquidActionOrder, walletAddress, signatureObj, orderAction, timestamp)
	requestBody, _ := json.Marshal(request)
	
	
	// 发送请求
	apiURL := fmt.Sprintf("%s%s", constants.HyperliquidRestBaseUrl, constants.HyperliquidExchangePath)
	headers := buildHeaders()
	
	responseBody, err := h.restClient.DoPostWithHeaders(apiURL, string(requestBody), headers)
	if err != nil {
		return nil, fmt.Errorf("place order failed: %w", err)
	}
	
	h.logger.Infof("[DEBUG] Response: %s", responseBody)
	
	// 解析响应
	return h.parseOrderResponse(responseBody, req)
}

// parseOrderResponse 解析下单响应
func (h *hyperliquidExchange) parseOrderResponse(responseBody string, req *model.PlaceOrderRequest) (*model.Order, error) {
	// 先尝试解析错误响应
	var errorResp struct {
		Status   string `json:"status"`
		Response string `json:"response"` // 错误时是字符串
	}
	
	if err := json.Unmarshal([]byte(responseBody), &errorResp); err == nil {
		if errorResp.Status == "err" {
			return nil, fmt.Errorf("hyperliquid API error: %s", errorResp.Response)
		}
	}
	
	// 解析成功响应
	var resp struct {
		Status string `json:"status"`
		Response struct {
			Type string `json:"type"`
			Data struct {
				Statuses []struct {
					Resting struct {
						Oid int64 `json:"oid"` // 订单 ID
					} `json:"resting"`
					Filled struct {
						TotalSz string `json:"totalSz"`
						AvgPx   string `json:"avgPx"`
					} `json:"filled"`
				} `json:"statuses"`
			} `json:"data"`
		} `json:"response"`
	}
	
	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse order response failed: %w", err)
	}
	
	if resp.Status != "ok" {
		return nil, fmt.Errorf("order failed: status=%s", resp.Status)
	}
	
	// 构建返回的订单对象
	order := &model.Order{
		Symbol:     req.Symbol,
		Side:       req.Side,
		Type:       req.Type,
		Quantity:   req.Quantity,
		Price:      req.Price,
		Status:     model.OrderStatusNew,
		CreateTime: time.Now(),
	}
	
	// 如果有订单状态信息，更新订单
	if len(resp.Response.Data.Statuses) > 0 {
		status := resp.Response.Data.Statuses[0]
		order.OrderID = fmt.Sprintf("%d", status.Resting.Oid)
		
		// 如果已成交
		if status.Filled.TotalSz != "" && status.Filled.TotalSz != "0" {
			order.FilledQty = parseFloat64(status.Filled.TotalSz)
			order.FilledPrice = parseFloat64(status.Filled.AvgPx)
			
			if order.FilledQty >= order.Quantity {
				order.Status = model.OrderStatusFilled
			} else {
				order.Status = model.OrderStatusPartiallyFilled
			}
		}
	}
	
	return order, nil
}
