package lighter

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// lighterAPIError Lighter API 错误响应
type lighterAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// signRequest 使用 HMAC SHA256 签名请求
func signRequest(message, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}

// normalizeLighterSymbol 标准化 Lighter 交易对符号
// 例如：BTCUSDT -> BTC-USDT
func normalizeLighterSymbol(symbol string) string {
	// Lighter 可能使用类似 BTC-USDT 的格式
	if strings.HasSuffix(symbol, "USDT") {
		return strings.TrimSuffix(symbol, "USDT") + "-USDT"
	}
	return symbol
}

// denormalizeSymbol 反标准化符号（转回系统格式）
func denormalizeSymbol(lighterSymbol string) string {
	// BTC-USDT -> BTCUSDT
	return strings.ReplaceAll(lighterSymbol, "-", "")
}

// formatQuantity 格式化数量
func formatQuantity(qty float64) string {
	return fmt.Sprintf("%.6f", qty)
}

// formatPrice 格式化价格
func formatPrice(price float64) string {
	return fmt.Sprintf("%.8f", price)
}

// getCurrentTimestamp 获取当前时间戳（毫秒）
func getCurrentTimestamp() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

// checkAPIError 检查 Lighter API 错误
func checkAPIError(responseBody string) error {
	var errorResp lighterAPIError
	if err := json.Unmarshal([]byte(responseBody), &errorResp); err == nil {
		if errorResp.Code != "" && errorResp.Code != "0" {
			return fmt.Errorf("lighter API error: code=%s, message=%s", errorResp.Code, errorResp.Message)
		}
	}
	return nil
}

// buildAuthHeaders 构建认证请求头
func buildAuthHeaders(apiKey, token string, nonce int64) map[string]string {
	headers := make(map[string]string)
	headers["X-API-KEY"] = apiKey
	headers["X-AUTH-TOKEN"] = token
	headers["X-NONCE"] = fmt.Sprintf("%d", nonce)
	headers["Content-Type"] = "application/json"
	return headers
}
