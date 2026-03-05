package binance

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// signRequest 生成请求签名
func signRequest(queryString string, secretKey string) string {
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(queryString))
	return hex.EncodeToString(mac.Sum(nil))
}

// buildQueryString 构建查询字符串（用于签名计算）
func buildQueryString(params map[string]string) string {
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

// buildHeaders 构建 Binance API 请求头
func buildHeaders(apiKey string) map[string]string {
	return map[string]string{
		"X-MBX-APIKEY": apiKey,
		"Content-Type": "application/json",
		"User-Agent":   "Mozilla/5.0 (compatible; Binance-Go-Client)",
	}
}

// formatQuantity 格式化数量，限制小数位数以避免精度错误
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

// checkAPIError 检查 Binance API 错误响应
func checkAPIError(responseBody string) error {
	var errorResp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal([]byte(responseBody), &errorResp); err == nil {
		if errorResp.Code != 0 || errorResp.Msg != "" {
			return fmt.Errorf("binance API error: code=%d, msg=%s", errorResp.Code, errorResp.Msg)
		}
	}
	return nil
}
