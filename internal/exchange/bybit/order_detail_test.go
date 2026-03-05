package bybit

import (
	"testing"

	"auto-arbitrage/internal/model"
)

// 表驱动测试：订单查询响应解析（QueryFuturesOrder/QuerySpotOrder 使用的解析逻辑）
func TestParseQueryOrderResponse(t *testing.T) {
	ex := &bybit{}

	tests := []struct {
		name        string
		body        string
		wantErr     bool
		wantOrderID string
		wantFilled  float64
		wantPrice   float64
		wantStatus  model.OrderStatus
	}{
		{
			name: "已成交单",
			body: `{"retCode":0,"retMsg":"OK","result":{"list":[{"orderId":"bybit-order-001","symbol":"BTCUSDT","side":"Buy","orderType":"Limit","orderStatus":"Filled","price":"50000","qty":"0.01","avgPrice":"50001.5","cumExecQty":"0.01","cumExecValue":"500.015","cumExecFee":"0.2","createdTime":"1627028708807","updatedTime":"1627028717807"}]}}`,
			wantErr:     false,
			wantOrderID: "bybit-order-001",
			wantFilled:  0.01,
			wantPrice:   50001.5,
			wantStatus:  model.OrderStatusFilled,
		},
		{
			name: "部分成交",
			body: `{"retCode":0,"retMsg":"OK","result":{"list":[{"orderId":"bybit-order-002","symbol":"ETHUSDT","side":"Sell","orderType":"Market","orderStatus":"PartiallyFilled","price":"0","qty":"1","avgPrice":"3500.2","cumExecQty":"0.5","cumExecValue":"1750.1","cumExecFee":"0.1","createdTime":"1627028708807","updatedTime":"1627028717807"}]}}`,
			wantErr:     false,
			wantOrderID: "bybit-order-002",
			wantFilled:  0.5,
			wantPrice:   3500.2,
			wantStatus:  model.OrderStatusPartiallyFilled,
		},
		{
			name:    "空列表返回错误",
			body:    `{"retCode":0,"retMsg":"OK","result":{"list":[]}}`,
			wantErr: true,
		},
		{
			name:    "API 错误",
			body:    `{"retCode":10001,"retMsg":"order not found"}`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ex.parseQueryOrderResponse(tt.body)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseQueryOrderResponse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if got.OrderID != tt.wantOrderID {
				t.Errorf("OrderID = %v, want %v", got.OrderID, tt.wantOrderID)
			}
			if got.FilledQty != tt.wantFilled {
				t.Errorf("FilledQty = %v, want %v", got.FilledQty, tt.wantFilled)
			}
			if got.FilledPrice != tt.wantPrice {
				t.Errorf("FilledPrice = %v, want %v", got.FilledPrice, tt.wantPrice)
			}
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %v, want %v", got.Status, tt.wantStatus)
			}
		})
	}
}
