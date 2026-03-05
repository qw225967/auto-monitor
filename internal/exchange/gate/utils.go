package gate

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// normalizeGateSymbol 归一化 Gate.io 交易对符号
// Gate.io 使用 BTC_USDT 格式
func normalizeGateSymbol(symbol string) string {
	s := strings.ToUpper(symbol)
	
	// 移除后缀
	s = strings.TrimSuffix(s, "-PERP")
	s = strings.TrimSuffix(s, "_PERP")
	s = strings.TrimSuffix(s, "-USDT-FUTURES")
	s = strings.TrimSuffix(s, "_USDT-FUTURES")
	s = strings.TrimSuffix(s, "-FUTURES")
	s = strings.TrimSuffix(s, "_FUTURES")
	
	// 转换为 Gate.io 格式：BTC_USDT
	if strings.Contains(s, "USDT") && !strings.Contains(s, "_") {
		// BTCUSDT -> BTC_USDT
		idx := strings.Index(s, "USDT")
		if idx > 0 {
			s = s[:idx] + "_" + s[idx:]
		}
	}
	
	// 将 - 替换为 _
	s = strings.ReplaceAll(s, "-", "_")
	
	return s
}

// signRequest 生成 Gate.io API 请求签名
// 签名算法：HMAC SHA512
func signRequest(method, url, queryString, payloadString, secretKey string, timestamp int64) string {
	// Gate.io 签名格式：
	// 步骤 1: 计算 payload 的 SHA512 哈希
	// 注意：即使 payload 为空，也需要计算空字符串的 SHA512 哈希
	// 空字符串的 SHA512 = cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e
	h := sha512.New()
	h.Write([]byte(payloadString))
	payloadHash := hex.EncodeToString(h.Sum(nil))

	// 步骤 2: 构建签名字符串
	signString := fmt.Sprintf("%s\n%s\n%s\n%s\n%d",
		method, url, queryString, payloadHash, timestamp)

	// 步骤 3: 使用 HMAC SHA512 生成签名
	mac := hmac.New(sha512.New, []byte(secretKey))
	mac.Write([]byte(signString))
	signature := hex.EncodeToString(mac.Sum(nil))
	return signature
}

// buildHeaders 构建 Gate.io API 请求头
func buildHeaders(apiKey, signature string, timestamp int64) map[string]string {
	return map[string]string{
		"KEY":          apiKey,
		"SIGN":         signature,
		"Timestamp":    strconv.FormatInt(timestamp, 10),
		"Content-Type": "application/json",
		"User-Agent":   "Mozilla/5.0 (compatible; Gate-Go-Client)",
	}
}

// formatQuantity 格式化数量
func formatQuantity(qty float64) string {
	if qty <= 0 {
		return "0"
	}

	var formatted string
	if qty >= 1.0 {
		formatted = strconv.FormatFloat(qty, 'f', 3, 64)
	} else {
		formatted = strconv.FormatFloat(qty, 'f', 6, 64)
	}

	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	return formatted
}

// formatPrice 格式化价格
func formatPrice(price float64) string {
	return strconv.FormatFloat(price, 'f', -1, 64)
}

// getCurrentTimestamp 获取当前时间戳（秒）
func getCurrentTimestamp() int64 {
	return time.Now().Unix()
}

// checkAPIError 检查 Gate.io API 错误响应
func checkAPIError(responseBody string) error {
	// Gate.io 错误格式：{"label":"ERROR_LABEL","message":"error message"}
	if strings.Contains(responseBody, `"label":`) && strings.Contains(responseBody, `"message":`) {
		return fmt.Errorf("gate API error: %s", responseBody)
	}
	return nil
}
