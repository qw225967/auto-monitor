package binance

import (
	"testing"

	"github.com/qw225967/auto-monitor/internal/model"
)

// 表驱动测试：订单响应解析（QueryFuturesOrder/QuerySpotOrder 复用的 parseOrderResponse）
func TestParseOrderResponse_QueryPath(t *testing.T) {
	ex := &binance{}

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
			name: "已成交 orderId 为数字",
			body: `{"orderId":12345678,"symbol":"BTCUSDT","side":"BUY","type":"LIMIT","status":"FILLED","price":"50000","avgPrice":"50001.5","origQty":"0.01","executedQty":"0.01","time":1627028708807,"updateTime":1627028717807}`,
			wantErr:     false,
			wantOrderID: "12345678",
			wantFilled:  0.01,
			wantPrice:   50001.5,
			wantStatus:  model.OrderStatusFilled,
		},
		{
			name: "已成交 orderId 为字符串",
			body: `{"orderId":"ord-str-001","symbol":"ETHUSDT","side":"SELL","type":"MARKET","status":"FILLED","price":"0","avgPrice":"3500.2","origQty":"1","executedQty":"1","time":1627028708807,"updateTime":1627028717807}`,
			wantErr:     false,
			wantOrderID: "ord-str-001",
			wantFilled:  1,
			wantPrice:   3500.2,
			wantStatus:  model.OrderStatusFilled,
		},
		{
			name: "部分成交",
			body: `{"orderId":999,"symbol":"BTCUSDT","side":"BUY","type":"LIMIT","status":"PARTIALLY_FILLED","price":"48000","avgPrice":"47950","origQty":"0.1","executedQty":"0.05","time":1627028708807,"updateTime":1627028717807}`,
			wantErr:     false,
			wantOrderID: "999",
			wantFilled:  0.05,
			wantPrice:   47950,
			wantStatus:  model.OrderStatusPartiallyFilled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ex.parseOrderResponse(tt.body, nil)
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
