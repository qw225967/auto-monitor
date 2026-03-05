package okx

import (
	"testing"

	"auto-arbitrage/internal/model"
)

// 表驱动测试：订单响应解析（QueryFuturesOrder/QuerySpotOrder 经 queryOrder 调用的 parseOrderResponse）
func TestParseOrderResponse_QueryPath(t *testing.T) {
	ex := &okx{}

	tests := []struct {
		name        string
		body        string
		req         *model.PlaceOrderRequest
		wantErr     bool
		wantOrderID string
		wantFilled  float64
		wantPrice   float64
		wantStatus  model.OrderStatus
	}{
		{
			name: "已成交 合约",
			body: `{"code":"0","msg":"","data":[{"ordId":"okx-ord-001","instId":"BTC-USDT-SWAP","side":"buy","ordType":"limit","state":"filled","px":"50000","avgPx":"50001.5","sz":"100","accFillSz":"100","fee":"-0.1","cTime":"1627028708807","uTime":"1627028717807"}]}`,
			req:         &model.PlaceOrderRequest{Symbol: "BTCUSDT"},
			wantErr:     false,
			wantOrderID: "okx-ord-001",
			wantFilled:  100,
			wantPrice:   50001.5,
			wantStatus:  model.OrderStatusFilled,
		},
		{
			name: "已成交 现货 req 提供 Symbol",
			body: `{"code":"0","msg":"","data":[{"ordId":"okx-spot-002","instId":"ETH-USDT","side":"sell","ordType":"market","state":"filled","px":"0","avgPx":"3500.2","sz":"1","accFillSz":"1","fee":"0.0035","cTime":"1627028708807","uTime":"1627028717807"}]}`,
			req:         &model.PlaceOrderRequest{Symbol: "ETHUSDT"},
			wantErr:     false,
			wantOrderID: "okx-spot-002",
			wantFilled:  1,
			wantPrice:   3500.2,
			wantStatus:  model.OrderStatusFilled,
		},
		{
			name: "部分成交",
			body: `{"code":"0","msg":"","data":[{"ordId":"okx-003","instId":"BTC-USDT-SWAP","side":"buy","ordType":"limit","state":"partially_filled","px":"48000","avgPx":"47950","sz":"10","accFillSz":"4","fee":"-0.01","cTime":"1627028708807","uTime":"1627028717807"}]}`,
			req:         &model.PlaceOrderRequest{Symbol: "BTCUSDT"},
			wantErr:     false,
			wantOrderID: "okx-003",
			wantFilled:  4,
			wantPrice:   47950,
			wantStatus:  model.OrderStatusPartiallyFilled,
		},
		{
			name:    "空 data 返回错误",
			body:    `{"code":"0","msg":"","data":[]}`,
			req:     &model.PlaceOrderRequest{Symbol: "BTCUSDT"},
			wantErr: true,
		},
		{
			name:    "API 错误",
			body:    `{"code":"50000","msg":"Order does not exist"}`,
			req:     nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ex.parseOrderResponse(tt.body, tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseOrderResponse() error = %v, wantErr %v", err, tt.wantErr)
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
