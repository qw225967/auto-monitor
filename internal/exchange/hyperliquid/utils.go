package hyperliquid

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// signMessage 使用 ED25519 私钥签名消息
// Hyperliquid 使用 Agent Wallet 的私钥进行签名
func signMessage(privateKeyHex string, message []byte) ([]byte, error) {
	// 去掉可能的 0x 前缀
	privateKeyHex = strings.TrimPrefix(privateKeyHex, "0x")
	privateKeyHex = strings.TrimPrefix(privateKeyHex, "0X")
	
	// 解析私钥（hex 格式）
	privateKeyBytes, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode private key: %w", err)
	}

	// ED25519 私钥应该是 64 字节（或者 32 字节的种子）
	var privateKey ed25519.PrivateKey
	if len(privateKeyBytes) == 32 {
		// 如果是 32 字节，当作种子生成私钥
		privateKey = ed25519.NewKeyFromSeed(privateKeyBytes)
	} else if len(privateKeyBytes) == 64 {
		// 如果是 64 字节，直接使用
		privateKey = ed25519.PrivateKey(privateKeyBytes)
	} else {
		return nil, fmt.Errorf("invalid private key length: %d (expected 32 or 64)", len(privateKeyBytes))
	}

	// 对消息进行哈希（Hyperliquid 可能需要，具体看 Python SDK）
	hasher := sha256.New()
	hasher.Write(message)
	messageHash := hasher.Sum(nil)

	// 使用 ED25519 签名
	signature := ed25519.Sign(privateKey, messageHash)
	return signature, nil
}

// signAction 签名交易 action（用于 /exchange 端点）
// 参考 Python SDK 的实现
func signAction(privateKeyHex string, action map[string]interface{}, nonce int64) (string, error) {
	// 构建签名消息
	// Hyperliquid 的签名格式：将 action 序列化为 JSON，然后添加 nonce 和 timestamp
	actionJSON, err := json.Marshal(action)
	if err != nil {
		return "", fmt.Errorf("failed to marshal action: %w", err)
	}

	// 构建签名消息（具体格式参考 Python SDK）
	message := fmt.Sprintf("%s:%d", string(actionJSON), nonce)
	
	// 签名
	signature, err := signMessage(privateKeyHex, []byte(message))
	if err != nil {
		return "", err
	}

	// 返回 hex 编码的签名
	return hex.EncodeToString(signature), nil
}

// normalizeSymbol 标准化交易对符号
// Hyperliquid 使用不同的符号格式，需要转换
// 例如：BTCUSDT -> BTC, ETHUSDT -> ETH
func normalizeSymbol(symbol string, isFutures bool) string {
	// Hyperliquid 的交易对格式
	// 现货：直接使用资产名称，如 "BTC"
	// 合约：使用资产名称，如 "BTC"
	
	// 移除 USDT 后缀
	symbol = strings.TrimSuffix(symbol, "USDT")
	symbol = strings.TrimSuffix(symbol, "USDC")
	symbol = strings.TrimSuffix(symbol, "USD")
	
	// 返回标准化后的符号
	return symbol
}

// denormalizeSymbol 反向转换符号（从 Hyperliquid 格式到系统格式）
func denormalizeSymbol(symbol string) string {
	// Hyperliquid: BTC -> BTCUSDT
	if !strings.HasSuffix(symbol, "USDT") && !strings.HasSuffix(symbol, "USD") {
		return symbol + "USDT"
	}
	return symbol
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

// getCurrentTimestamp 获取当前时间戳（毫秒）
func getCurrentTimestamp() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

// getCurrentNonce 获取当前 nonce（基于时间戳）
func getCurrentNonce() int64 {
	return getCurrentTimestamp()
}

// buildInfoRequest 构建 Info API 请求体
func buildInfoRequest(queryType string, params map[string]interface{}) map[string]interface{} {
	request := map[string]interface{}{
		"type": queryType,
	}
	
	// 合并参数
	for k, v := range params {
		request[k] = v
	}
	
	return request
}

// buildExchangeRequest 构建 Exchange API 请求体（需要签名）
func buildExchangeRequest(action string, walletAddress string, signature string, params map[string]interface{}) map[string]interface{} {
	// 构建 action 对象
	actionMap := map[string]interface{}{
		"type": action,
	}
	
	// 将 params 合并到 action 中
	for k, v := range params {
		actionMap[k] = v
	}
	
	// 构建完整请求
	request := map[string]interface{}{
		"action":      actionMap,
		"nonce":       getCurrentNonce(),
		"signature":   map[string]interface{}{
			"r": signature[:64],  // 前32字节
			"s": signature[64:],  // 后32字节
			"v": 27,              // 或 28
		},
		"vaultAddress": nil,      // 如果不使用 vault，设为 null
	}
	
	return request
}

// checkAPIError 检查 Hyperliquid API 错误响应
func checkAPIError(responseBody string) error {
	// Hyperliquid 的错误响应格式
	var errorResp struct {
		Error string `json:"error"`
	}
	
	if err := json.Unmarshal([]byte(responseBody), &errorResp); err == nil {
		if errorResp.Error != "" {
			return fmt.Errorf("hyperliquid API error: %s", errorResp.Error)
		}
	}
	
	// 检查是否有 status 字段表示错误
	var statusResp struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	
	if err := json.Unmarshal([]byte(responseBody), &statusResp); err == nil {
		if statusResp.Status == "error" || statusResp.Error != "" {
			return fmt.Errorf("hyperliquid API error: %s", statusResp.Error)
		}
	}
	
	return nil
}

// buildHeaders 构建 HTTP 请求头
func buildHeaders() map[string]string {
	return map[string]string{
		"Content-Type": "application/json",
		"User-Agent":   "Mozilla/5.0 (compatible; Hyperliquid-Go-Client)",
	}
}

// parseFloat64 安全地解析 float64（处理字符串和数字）
func parseFloat64(value interface{}) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	default:
		return 0
	}
}

// parseInt64 安全地解析 int64（处理字符串和数字）
func parseInt64(value interface{}) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case string:
		i, _ := strconv.ParseInt(v, 10, 64)
		return i
	default:
		return 0
	}
}
