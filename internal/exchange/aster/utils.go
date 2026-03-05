package aster

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// asterAPIError Aster API 错误响应
type asterAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"msg"`
}

// signRequest 使用 HMAC SHA256 签名请求（类似 Binance）
func signRequest(message, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}

// normalizeAsterSymbol 标准化 Aster 交易对符号
func normalizeAsterSymbol(symbol string) string {
	// Aster 可能使用 BTCUSDT 格式（类似 Binance）
	return symbol
}

// formatQuantity 格式化数量
func formatQuantity(qty float64) string {
	return fmt.Sprintf("%.6f", qty)
}

// formatPrice 格式化价格
func formatPrice(price float64) string {
	return fmt.Sprintf("%.8f", price)
}

// checkAPIError 检查 Aster API 错误
func checkAPIError(responseBody string) error {
	var errorResp asterAPIError
	if err := json.Unmarshal([]byte(responseBody), &errorResp); err == nil {
		if errorResp.Code != 0 && errorResp.Code != 200 {
			return fmt.Errorf("aster API error: code=%d, msg=%s", errorResp.Code, errorResp.Message)
		}
	}
	return nil
}
