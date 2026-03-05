package bitget

import (
	"testing"

	"auto-arbitrage/internal/model"
)

// 表驱动测试：合约订单详情解析（QueryFuturesOrder 使用的解析逻辑）
func TestParseFuturesOrderDetailResponse(t *testing.T) {
	ex := &bitgetExchange{}

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
			name: "v2 风格 filledQty 已成交",
			body: `{"code":"00000","msg":"success","data":{"orderId":"802382049422487552","symbol":"BTCUSDT_UMCBL","side":"open_long","orderType":"limit","price":"23999.3","size":"1","status":"full_fill","fillPrice":"24001.2","filledQty":"1","fee":"0.5","cTime":"1627028708807","uTime":"1627028717807"}}`,
			wantErr:     false,
			wantOrderID: "802382049422487552",
			wantFilled:  1,
			wantPrice:   24001.2,
			wantStatus:  model.OrderStatusFilled,
		},
		{
			name: "v1 风格 baseVolume 已成交",
			body: `{"code":"00000","msg":"success","data":{"orderId":"802382049422487553","symbol":"ETHUSDT_UMCBL","side":"close_short","orderType":"market","price":"0","size":"0.1","state":"filled","fillPrice":"3500.5","baseVolume":"0.1","fee":"0.01","cTime":"1627028708807","uTime":"1627028717807"}}`,
			wantErr:     false,
			wantOrderID: "802382049422487553",
			wantFilled:  0.1,
			wantPrice:   3500.5,
			wantStatus:  model.OrderStatusFilled,
		},
		{
			name: "filledQty 优先于 baseVolume",
			body: `{"code":"00000","msg":"success","data":{"orderId":"id1","symbol":"XRPUSDT_UMCBL","side":"open_long","orderType":"limit","price":"1.2","size":"100","status":"full_fill","fillPrice":"1.21","filledQty":"100","baseVolume":"99","fee":"0","cTime":"1627028708807","uTime":"1627028717807"}}`,
			wantErr:     false,
			wantOrderID: "id1",
			wantFilled:  100,
			wantPrice:   1.21,
			wantStatus:  model.OrderStatusFilled,
		},
		{
			name: "V2 仅有 priceAvg 无 fillPrice",
			body: `{"code":"00000","msg":"success","data":{"orderId":"1411554763236589577","symbol":"COAIUSDT","side":"sell","orderType":"market","price":"0","size":"30","state":"filled","priceAvg":"0.12345","baseVolume":"30","fee":"0.005391","cTime":"1730182801000","uTime":"1730182801000"}}`,
			wantErr:     false,
			wantOrderID: "1411554763236589577",
			wantFilled:  30,
			wantPrice:   0.12345,
			wantStatus:  model.OrderStatusFilled,
		},
		{
			name:    "API 错误码",
			body:    `{"code":"40001","msg":"param error"}`,
			wantErr: true,
		},
		{
			name: "部分成交 status",
			body: `{"code":"00000","msg":"success","data":{"orderId":"id2","symbol":"BTCUSDT_UMCBL","side":"open_long","orderType":"limit","price":"23000","size":"2","status":"partial_fill","fillPrice":"23001","filledQty":"1","fee":"0.1","cTime":"1627028708807","uTime":"1627028717807"}}`,
			wantErr:     false,
			wantOrderID: "id2",
			wantFilled:  1,
			wantPrice:   23001,
			wantStatus:  model.OrderStatusPartiallyFilled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ex.parseFuturesOrderDetailResponse(tt.body)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseFuturesOrderDetailResponse() error = %v, wantErr %v", err, tt.wantErr)
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

// 表驱动测试：现货订单详情解析（QuerySpotOrder 使用的解析逻辑）
func TestParseSpotOrderDetailResponse(t *testing.T) {
	ex := &bitgetExchange{}

	tests := []struct {
		name        string
		body        string
		wantErr     bool
		wantOrderID string
		wantFilled  float64
		wantStatus  model.OrderStatus
		wantFee     *float64 // 非 nil 时断言 Fee
	}{
		{
			name: "单条已成交（旧版 fillPrice/fillQuantity）",
			body: `{"code":"00000","msg":"success","data":[{"orderId":"spot123","symbol":"BTCUSDT","side":"buy","orderType":"market","price":"0","size":"0.01","status":"full_fill","fillPrice":"43210.5","fillQuantity":"0.01","fee":"0.43","cTime":"1627028708807","uTime":"1627028717807"}]}`,
			wantErr:     false,
			wantOrderID: "spot123",
			wantFilled:  0.01,
			wantStatus:  model.OrderStatusFilled,
		},
		{
			name: "V2 已成交（priceAvg/baseVolume/status=filled）",
			body: `{"code":"00000","msg":"success","data":[{"orderId":"1411532188575424516","symbol":"POWERUSDT","side":"sell","orderType":"market","price":"0","size":"4.97","status":"filled","priceAvg":"0.1234","baseVolume":"4.97","quoteVolume":"0.61","cTime":"1730182219000","uTime":"1730182219500"}]}`,
			wantErr:     false,
			wantOrderID: "1411532188575424516",
			wantFilled:  4.97,
			wantStatus:  model.OrderStatusFilled,
		},
		{
			name: "V2 已成交且含 feeDetail（newFees.t）",
			body: `{"code":"00000","msg":"success","data":[{"orderId":"ord123","symbol":"POWERUSDT","side":"sell","orderType":"market","price":"0","size":"10","status":"filled","priceAvg":"1.5","baseVolume":"10","quoteVolume":"15","feeDetail":"{\"newFees\":{\"t\":-0.015,\"r\":-0.015,\"c\":0,\"d\":0,\"deduction\":false,\"totalDeductionFee\":0}}","cTime":"1730182219000","uTime":"1730182219500"}]}`,
			wantErr:     false,
			wantOrderID: "ord123",
			wantFilled:  10,
			wantStatus:  model.OrderStatusFilled,
			wantFee:     float64Ptr(0.015),
		},
		{
			name:    "空列表返回错误",
			body:    `{"code":"00000","msg":"success","data":[]}`,
			wantErr: true,
		},
		{
			name:    "API 错误",
			body:    `{"code":"40002","msg":"order not found"}`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ex.parseSpotOrderDetailResponse(tt.body)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseSpotOrderDetailResponse() error = %v, wantErr %v", err, tt.wantErr)
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
			if tt.wantFee != nil && got.Fee != *tt.wantFee {
				t.Errorf("Fee = %v, want %v", got.Fee, *tt.wantFee)
			}
		})
	}
}

func float64Ptr(f float64) *float64 { return &f }
