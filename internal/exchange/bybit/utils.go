package bybit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// normalizeBybitSymbol 归一化 Bybit 交易对符号
// 例如：BTCUSDT-PERP -> BTCUSDT, BTC_USDT -> BTCUSDT
func normalizeBybitSymbol(symbol string) string {
	s := strings.ToUpper(symbol)
	s = strings.TrimSuffix(s, "-PERP")
	s = strings.TrimSuffix(s, "_PERP")
	s = strings.TrimSuffix(s, "-USDT-FUTURES")
	s = strings.TrimSuffix(s, "_USDT-FUTURES")
	s = strings.TrimSuffix(s, "-FUTURES")
	s = strings.TrimSuffix(s, "_FUTURES")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}

// signRequest 生成 Bybit API V5 请求签名
// Bybit V5 签名规则：HMAC SHA256(timestamp + apiKey + recvWindow + queryString/body)
func signRequest(timestamp, apiKey, recvWindow, paramStr, secretKey string) string {
	message := timestamp + apiKey + recvWindow + paramStr
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

// buildQueryString 构建查询字符串（用于签名计算和 URL）
// 参数按字母顺序排序
func buildQueryString(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, url.QueryEscape(params[k])))
	}
	return strings.Join(parts, "&")
}

// buildFormData 构建表单数据（signature 必须放在最后）
func buildFormData(params map[string]string) string {
	keys := make([]string, 0, len(params))
	var signatureValue string
	var hasSignature bool

	for k := range params {
		if k == "signature" {
			signatureValue = params[k]
			hasSignature = true
		} else {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, url.QueryEscape(params[k])))
	}

	if hasSignature {
		parts = append(parts, fmt.Sprintf("signature=%s", url.QueryEscape(signatureValue)))
	}

	return strings.Join(parts, "&")
}

// buildHeaders 构建 Bybit API V5 请求头
func buildHeaders(apiKey, timestamp, signature, recvWindow string) map[string]string {
	return map[string]string{
		"X-BAPI-API-KEY":     apiKey,
		"X-BAPI-TIMESTAMP":   timestamp,
		"X-BAPI-SIGN":        signature,
		"X-BAPI-SIGN-TYPE":   "2", // HMAC SHA256
		"X-BAPI-RECV-WINDOW": recvWindow,
		"Content-Type":       "application/json",
	}
}

// formatQuantity 格式化数量，限制小数位数以避免精度错误
func formatQuantity(qty float64) string {
	if qty <= 0 {
		return "0"
	}

	var formatted string
	if qty >= 1.0 {
		// 大于等于 1，保留 3 位小数
		formatted = strconv.FormatFloat(qty, 'f', 3, 64)
	} else {
		// 小于 1，保留 6 位小数
		formatted = strconv.FormatFloat(qty, 'f', 6, 64)
	}

	// 去除尾部的 0
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	return formatted
}

// formatPrice 格式化价格
func formatPrice(price float64) string {
	return strconv.FormatFloat(price, 'f', -1, 64)
}

// checkAPIError 检查 Bybit API 错误响应
func checkAPIError(responseBody string) error {
	// Bybit API 错误格式：{"retCode":10001,"retMsg":"error message"}
	// 如果 retCode != 0，表示有错误

	// 简单检查是否包含错误标识
	if strings.Contains(responseBody, `"retCode":`) && !strings.Contains(responseBody, `"retCode":0`) {
		// 提取错误信息（简化版，实际应该用 JSON 解析）
		return fmt.Errorf("bybit API error: %s", responseBody)
	}
	return nil
}
