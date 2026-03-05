package bitget

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// normalizeBitgetSymbol 归一化 Bitget 交易对符号
// V2 API 使用统一格式：现货和合约都是 BTCUSDT (不再使用 _UMCBL 后缀)
// V1 使用: 现货 BTCUSDT，合约 BTCUSDT_UMCBL
// V2 使用: 现货 BTCUSDT，合约 BTCUSDT
func normalizeBitgetSymbol(symbol string, isFutures bool) string {
	s := strings.ToUpper(symbol)
	
	// 移除所有后缀（兼容 V1 格式）
	s = strings.TrimSuffix(s, "-PERP")
	s = strings.TrimSuffix(s, "_PERP")
	s = strings.TrimSuffix(s, "-USDT-FUTURES")
	s = strings.TrimSuffix(s, "_USDT-FUTURES")
	s = strings.TrimSuffix(s, "-FUTURES")
	s = strings.TrimSuffix(s, "_FUTURES")
	s = strings.TrimSuffix(s, "_UMCBL") // V1 合约后缀
	
	// 移除分隔符
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	
	// V2 API 不再需要添加后缀，现货和合约都使用统一格式
	// 例如：BTCUSDT (现货和合约通用)
	
	return s
}

// buildLoginMessage 构建 WebSocket 登录消息
func buildLoginMessage(apiKey, secretKey, passphrase string) string {
	timestamp := time.Now().Unix()
	message := fmt.Sprintf("%d%s%s", timestamp, "GET", "/user/verify")
	
	signature := signHmacSha256(message, secretKey)
	
	loginMsg := map[string]interface{}{
		"op": "login",
		"args": []map[string]interface{}{
			{
				"apiKey":     apiKey,
				"passphrase": passphrase,
				"timestamp":  strconv.FormatInt(timestamp, 10),
				"sign":       signature,
			},
		},
	}
	
	jsonData, _ := json.Marshal(loginMsg)
	return string(jsonData)
}

// buildSubscribeMessage 构建订阅消息
// channel: V2 API 支持的频道如 "ticker", "trade", "books" 等
func buildSubscribeMessage(symbols []string, marketType, channel string) string {
	args := make([]map[string]string, 0, len(symbols))
	
	// V2 API 的 instType
	// spot: SPOT, futures: USDT-FUTURES/USDC-FUTURES/COIN-FUTURES
	instType := "SPOT"
	if marketType == "futures" {
		instType = "USDT-FUTURES" // V2 使用完整名称
	}
	
	for _, symbol := range symbols {
		normalizedSymbol := normalizeBitgetSymbol(symbol, marketType == "futures")
		args = append(args, map[string]string{
			"instType": instType,
			"channel":  channel,
			"instId":   normalizedSymbol,
		})
	}
	
	subMsg := map[string]interface{}{
		"op":   "subscribe",
		"args": args,
	}
	
	jsonData, _ := json.Marshal(subMsg)
	return string(jsonData)
}

// signHmacSha256 HMAC SHA256 签名
func signHmacSha256(message, secretKey string) string {
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// signRequest 生成 Bitget API 请求签名
func signRequest(timestamp, method, requestPath, queryString, body, secretKey string) string {
	message := timestamp + method + requestPath
	if queryString != "" {
		message += "?" + queryString
	}
	if body != "" {
		message += body
	}
	return signHmacSha256(message, secretKey)
}

// buildHeaders 构建 Bitget API 请求头
func buildHeaders(apiKey, signature, timestamp, passphrase string) map[string]string {
	return map[string]string{
		"ACCESS-KEY":       apiKey,
		"ACCESS-SIGN":      signature,
		"ACCESS-TIMESTAMP": timestamp,
		"ACCESS-PASSPHRASE": passphrase,
		"Content-Type":     "application/json",
		"locale":           "en-US",
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
// Bitget 现货要求 price 符合 symbol 的 pricePrecision，否则 41103 param price scale error
// 按价格区间取合理小数位：>=100 取 2 位，>=1 取 4 位，>=0.01 取 6 位，否则 8 位
func formatPrice(price float64) string {
	decimals := 8
	switch {
	case price >= 100:
		decimals = 2
	case price >= 1:
		decimals = 4
	case price >= 0.01:
		decimals = 6
	}
	pow := math.Pow(10, float64(decimals))
	rounded := math.Round(price*pow) / pow
	return strconv.FormatFloat(rounded, 'f', decimals, 64)
}

// getCurrentTimestamp 获取当前时间戳（毫秒）
func getCurrentTimestamp() string {
	return strconv.FormatInt(time.Now().UnixMilli(), 10)
}

// checkAPIError 检查 Bitget API 错误响应
func checkAPIError(responseBody string) error {
	// Bitget 错误格式：{"code":"40001","msg":"error message"}
	if strings.Contains(responseBody, `"code":`) && !strings.Contains(responseBody, `"code":"00000"`) {
		return fmt.Errorf("bitget API error: %s", responseBody)
	}
	return nil
}
