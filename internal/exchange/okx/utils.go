package okx

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qw225967/auto-monitor/constants"
)

// BuildOKXSign 构建 OKX API 签名
// timestamp: ISO 8601 格式 "2006-01-02T15:04:05.000Z"
// method: HTTP 方法 (GET, POST)
// requestPath: API 路径 (如 "/api/v5/asset/deposit-address")
// body: 请求体（GET 请求为空字符串）
// secretKey: API Secret Key
func BuildOKXSign(timestamp, method, requestPath, body, secretKey string) string {
	message := timestamp + method + requestPath + body
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// GetOKXTimestamp 获取 OKX API 时间戳（ISO 8601 格式）
func GetOKXTimestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// BuildOKXHeaders 构建 OKX API 请求头
func BuildOKXHeaders(apiKey, sign, timestamp, passphrase string) map[string]string {
	return map[string]string{
		constants.OkexDexBgAccessKey:        apiKey,
		constants.OkexDexBgAccessSign:       sign,
		constants.OkexDexBgAccessTimestamp:  timestamp,
		constants.OkexDexBgAccessPassphrase: passphrase,
		constants.OkexDexContentType:        constants.OkexDexApplicationJson,
	}
}

// CheckOKXAPIError 检查 OKX API 错误响应（code 非 "0" 表示错误）
func CheckOKXAPIError(responseBody string) error {
	code, msg, sCode, sMsg := extractOKXErrorDetails(responseBody)
	if code != "" && code != "0" {
		if sCode != "" || sMsg != "" {
			return fmt.Errorf("okx API error: code=%s, msg=%s, sCode=%s, sMsg=%s", code, msg, sCode, sMsg)
		}
		return fmt.Errorf("okx API error: code=%s, msg=%s", code, msg)
	}
	// OKX 有时顶层 code=0，但 data 里的 sCode!=0 表示该笔操作失败；sMsg 非空不视为错误（成功时可能为 "Order placed" 等）
	if sCode != "" && sCode != "0" {
		return fmt.Errorf("okx API error: code=%s, msg=%s, sCode=%s, sMsg=%s", code, msg, sCode, sMsg)
	}
	return nil
}

func extractOKXErrorDetails(responseBody string) (code, msg, sCode, sMsg string) {
	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			SCode string `json:"sCode"`
			SMsg  string `json:"sMsg"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return "", "", "", ""
	}
	code = resp.Code
	msg = resp.Msg
	if len(resp.Data) > 0 {
		sCode = resp.Data[0].SCode
		sMsg = resp.Data[0].SMsg
	}
	return code, msg, sCode, sMsg
}

func debugLogOKX(location, message string, data map[string]interface{}, hypothesisId, runId string) {}

// ToOKXSpotInstId 将统一符号转为 OKX 现货 instId（如 BTCUSDT -> BTC-USDT）
func ToOKXSpotInstId(symbol string) string {
	return toOKXInstId(symbol, "")
}

// ToOKXSwapInstId 将统一符号转为 OKX 永续合约 instId（如 BTCUSDT -> BTC-USDT-SWAP）
func ToOKXSwapInstId(symbol string) string {
	return toOKXInstId(symbol, "-SWAP")
}

// toOKXInstId 统一符号转 OKX instId，suffix 如 "" 或 "-SWAP"
func toOKXInstId(symbol string, suffix string) string {
	symbol = strings.TrimSpace(strings.ToUpper(symbol))
	if symbol == "" {
		return symbol
	}
	// 常见 quote: USDT, USDC, USD
	for _, q := range []string{"USDT", "USDC", "USD", "BTC", "ETH"} {
		if strings.HasSuffix(symbol, q) {
			base := strings.TrimSuffix(symbol, q)
			if base != "" {
				return base + "-" + q + suffix
			}
			break
		}
	}
	// 默认按最后 4 位为 USDT
	if len(symbol) > 4 && strings.HasSuffix(symbol, "USDT") {
		return symbol[:len(symbol)-4] + "-USDT" + suffix
	}
	return symbol + suffix
}

// FromOKXInstId 将 OKX instId 转为统一符号（如 BTC-USDT 或 BTC-USDT-SWAP -> BTCUSDT）
func FromOKXInstId(instId string) string {
	s := strings.TrimSpace(instId)
	s = strings.TrimSuffix(s, "-SWAP")
	return strings.ReplaceAll(s, "-", "")
}

// FormatQuantity 格式化数量，限制小数位（与 binance 一致，便于下单）
func FormatQuantity(qty float64) string {
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

// FormatPrice 格式化价格
func FormatPrice(price float64) string {
	return strconv.FormatFloat(price, 'f', -1, 64)
}
