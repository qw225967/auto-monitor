package gate

import (
	"testing"

	"github.com/qw225967/auto-monitor/internal/model"
)

func TestParseSpotOrderResponse(t *testing.T) {
	ex := &gateExchange{}

	tests := []struct {
		name        string
		body        string
		wantErr     bool
		wantOrderID string
		wantFilled  float64
		wantStatus  model.OrderStatus
	}{
		{
			name:        "已成交",
			body:        `{"id":"spot-001","text":"","create_time":"1627028708","update_time":"1627028717","currency_pair":"BTC_USDT","status":"closed","type":"market","account":"spot","side":"buy","amount":"0.01","price":"0","filled_amount":"0.01","filled_total":"500.5","avg_deal_price":"50050","fee":"0.5","fee_currency":"USDT"}`,
			wantErr:     false,
			wantOrderID: "spot-001",
			wantFilled:  0.01,
			wantStatus:  model.OrderStatusFilled,
		},
		{
			name:        "未成交 open",
			body:        `{"id":"spot-002","text":"","create_time":"1627028708","update_time":"1627028708","currency_pair":"ETH_USDT","status":"open","type":"limit","account":"spot","side":"sell","amount":"1","price":"3500","filled_amount":"0","filled_total":"0","avg_deal_price":"0","fee":"0","fee_currency":"USDT"}`,
			wantErr:     false,
			wantOrderID: "spot-002",
			wantFilled:  0,
			wantStatus:  model.OrderStatusNew,
		},
		{
			name:    "API 错误",
			body:    `{"label":"INVALID_ORDER","message":"Order not found"}`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ex.parseSpotOrderResponse(tt.body)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseSpotOrderResponse() error = %v, wantErr %v", err, tt.wantErr)
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
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %v, want %v", got.Status, tt.wantStatus)
			}
		})
	}
}

func TestParseFuturesOrderResponse(t *testing.T) {
	ex := &gateExchange{}

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
			name:        "已成交",
			body:        `{"id":80123456,"user":100001,"create_time":1627028708.5,"finish_time":1627028717.5,"contract":"BTC_USDT","size":100,"price":"50000","fill_price":"50001.5","left":0,"status":"finished"}`,
			wantErr:     false,
			wantOrderID: "80123456",
			wantFilled:  100,
			wantPrice:   50001.5,
			wantStatus:  model.OrderStatusFilled,
		},
		{
			name:        "部分成交",
			body:        `{"id":80123457,"user":100001,"create_time":1627028708,"finish_time":1627028710,"contract":"ETH_USDT","size":10,"price":"3500","fill_price":"3501","left":5,"status":"open"}`,
			wantErr:     false,
			wantOrderID: "80123457",
			wantFilled:  5,
			wantPrice:   3501,
			wantStatus:  model.OrderStatusNew,
		},
		{
			name:    "API 错误",
			body:    `{"label":"INVALID_ORDER","message":"Order not found"}`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ex.parseFuturesOrderResponse(tt.body)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseFuturesOrderResponse() error = %v, wantErr %v", err, tt.wantErr)
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
