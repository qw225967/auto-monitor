package hyperliquid

// buildExchangeRequestWithEIP712 使用 EIP-712 签名构建交易所请求
func buildExchangeRequestWithEIP712(action string, userAddress string, signature map[string]interface{}, actionData interface{}, nonce int64) map[string]interface{} {
	// 构建完整请求
	// Hyperliquid API Wallet 模式需要指定用户地址（小写）
	request := map[string]interface{}{
		"action":       actionData, // 直接使用传入的 action 数据
		"nonce":        nonce,
		"signature":    signature,
		"vaultAddress": nil,
	}
	
	return request
}
