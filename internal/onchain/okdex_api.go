package onchain

import (
	"auto-arbitrage/constants"
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"
)

// buildLoginSign 构建登录签名
func (o *okdex) buildLoginSign(timestamp, method, routerPath, body, secret string) string {
	msg := timestamp + method + routerPath + body
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// getNextAppKey 获取下一个 API Key
func (o *okdex) getNextAppKey(canBroadcast bool) (model.OkexKeyRecord, error) {
	manager := config.GetOkexKeyManager()
	return manager.GetNextAppKey(canBroadcast)
}

// getAppKeyByIndex 根据索引获取 API Key
func (o *okdex) getAppKeyByIndex(index int) (model.OkexKeyRecord, error) {
	manager := config.GetOkexKeyManager()
	return manager.GetKeyByIndex(index)
}

// restQueryServerTimestamp 查询服务器时间戳
func (o *okdex) restQueryServerTimestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// doAPIRequest 执行 API 请求的通用方法
func (o *okdex) doAPIRequest(method, path string, values url.Values, body string) (string, error) {
	baseURL := strings.TrimRight(constants.OkexDexBaseUrl, "/")
	record, err := o.getNextAppKey(false)
	if err != nil {
		return "", err
	}

	ts := o.restQueryServerTimestamp()
	queryStr := ""
	if values != nil {
		queryStr = "?" + values.Encode()
	}
	sign := o.buildLoginSign(ts, method, path, queryStr+body, record.SecretKey)

	fullURL := fmt.Sprintf("%s/%s%s", baseURL, strings.TrimLeft(path, "/"), queryStr)

	if method == constants.HttpMethodGet {
		return o.restClient.DoGet(constants.ConnectTypeOKEX, fullURL, body, record.AppKey, sign, record.Passphrase, ts)
	}
	return o.restClient.DoPost(constants.ConnectTypeOKEX, fullURL, body, record.AppKey, sign, record.Passphrase, ts)
}

// queryDexSwap 查询兑换交易
func (o *okdex) queryDexSwap(fromTokenContractAddress, toTokenContractAddress, swapMode, chainIndex, amount, slippage,
	walletAddress, gasLimit string) (model.OkexDexSwapResponse, error) {

	values := url.Values{}
	values.Add("amount", amount)
	values.Add("chainIndex", chainIndex)
	values.Add("toTokenAddress", toTokenContractAddress)
	values.Add("fromTokenAddress", fromTokenContractAddress)
	values.Add("swapMode", swapMode)
	values.Add("userWalletAddress", walletAddress)
	values.Add("gaslimit", gasLimit)
	values.Add("gasLevel", "average")
	values.Add("autoSlippage", "false")
	values.Add("slippagePercent", slippage)

	resp, err := o.doAPIRequest(constants.HttpMethodGet, constants.OkexDexSwap, values, "")
	if err != nil {
		return model.OkexDexSwapResponse{}, fmt.Errorf("request failed: %w", err)
	}

	var swapData model.OkexDexSwapResponse
	if err := json.Unmarshal([]byte(resp), &swapData); err != nil {
		return model.OkexDexSwapResponse{}, fmt.Errorf("unmarshal failed: %w", err)
	}

	if swapData.Code != "" && swapData.Code != "0" {
		return model.OkexDexSwapResponse{}, fmt.Errorf("API error: code=%s, msg=%s", swapData.Code, swapData.Msg)
	}

	record, _ := o.getNextAppKey(false)
	swapData.Index = record.Index
	return swapData, nil
}

// queryAllTokenBalances 查询地址持有的多个链或指定链的代币余额列表
func (o *okdex) queryAllTokenBalances(address, chains string, excludeRiskToken string) (model.OkexTokenBalanceResponse, error) {
	values := url.Values{}
	values.Add("address", address)
	values.Add("chains", chains)
	if excludeRiskToken != "" {
		values.Add("excludeRiskToken", excludeRiskToken)
	}

	resp, err := o.doAPIRequest(constants.HttpMethodGet, constants.OkexDexAllTokenBalancesByAddress, values, "")
	if err != nil {
		return model.OkexTokenBalanceResponse{}, fmt.Errorf("request failed: %w", err)
	}

	var balanceData model.OkexTokenBalanceResponse
	if err := json.Unmarshal([]byte(resp), &balanceData); err != nil {
		return model.OkexTokenBalanceResponse{}, fmt.Errorf("unmarshal failed: %w", err)
	}

	if balanceData.Code != "" && balanceData.Code != "0" {
		return model.OkexTokenBalanceResponse{}, fmt.Errorf("API error: code=%s, msg=%s", balanceData.Code, balanceData.Msg)
	}

	return balanceData, nil
}

// queryApproveTransaction 查询授权交易数据
func (o *okdex) queryApproveTransaction(tokenAddress, chainIndex, walletAddress string) (model.OkexDexApproveTransactionResponse, error) {
	values := url.Values{}
	values.Add("chainIndex", chainIndex)
	values.Add("tokenContractAddress", tokenAddress)
	values.Add("userWalletAddress", walletAddress)
	maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	values.Add("approveAmount", maxUint256.String())

	resp, err := o.doAPIRequest(constants.HttpMethodGet, constants.OkexDexApproveTransaction, values, "")
	if err != nil {
		return model.OkexDexApproveTransactionResponse{}, fmt.Errorf("request failed: %w", err)
	}

	var approveData model.OkexDexApproveTransactionResponse
	if err := json.Unmarshal([]byte(resp), &approveData); err != nil {
		return model.OkexDexApproveTransactionResponse{}, fmt.Errorf("unmarshal failed: %w", err)
	}

	if approveData.Code != "" && approveData.Code != "0" && approveData.Code != "82000" {
		return model.OkexDexApproveTransactionResponse{}, fmt.Errorf("API error: code=%s, msg=%s", approveData.Code, approveData.Msg)
	}

	return approveData, nil
}

// fetchNonceFromServer 从服务器获取 nonce
func (o *okdex) fetchNonceFromServer(walletAddress, chainIndex string) (string, error) {
	values := url.Values{}
	values.Add("chainIndex", chainIndex)
	values.Add("address", walletAddress)

	resp, err := o.doAPIRequest(constants.HttpMethodGet, constants.OkexDexNonce, values, "")
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}

	respTrimmed := strings.TrimSpace(resp)
	if strings.HasPrefix(respTrimmed, "<!DOCTYPE") || strings.HasPrefix(respTrimmed, "<html") {
		return "", fmt.Errorf("API returned HTML instead of JSON")
	}

	var nonceResp model.OkexNonceResponse
	if err := json.Unmarshal([]byte(resp), &nonceResp); err != nil {
		return "", fmt.Errorf("unmarshal failed: %w", err)
	}

	if nonceResp.Code != "0" {
		return "", fmt.Errorf("API error: code=%s, msg=%s", nonceResp.Code, nonceResp.Msg)
	}

	if len(nonceResp.Data) == 0 {
		return "", errors.New("no nonce data returned")
	}

	return nonceResp.Data[0].Nonce, nil
}

// broadcastTransaction 广播签名后的交易到区块链
func (o *okdex) broadcastTransaction(signedTx, chainIndex, walletAddress string, index int, actualTxHash string) (string, error) {
	log := logger.GetLoggerInstance().Named("okdex").Sugar()

	o.mu.RLock()
	useBundler := o.useBundler
	bundlerMgr := o.bundlerManager
	o.mu.RUnlock()

	if useBundler && bundlerMgr != nil {
		bundlerInstance, err := bundlerMgr.GetBundler(chainIndex)
		if err != nil {
			log.Errorf("broadcastTransaction - Failed to get bundler for chain %s: %v", chainIndex, err)
			return "", fmt.Errorf("failed to get bundler: %w", err)
		}

		bundleHash, err := bundlerInstance.SendBundle(signedTx, chainIndex)
		if err != nil {
			log.Errorf("broadcastTransaction - Bundle send failed: %v", err)
			return "", fmt.Errorf("bundle send failed: %w", err)
		}

		// Bundle 成功
		if actualTxHash != "" {
			return actualTxHash, nil
		}
		return bundleHash, nil
	}
	reqBody := model.OkexBroadcastTxRequest{
		SignedTx:   signedTx,
		ChainIndex: chainIndex,
		Address:    walletAddress,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal body failed: %v", err)
	}

	record, err := o.getAppKeyByIndex(index)
	if err != nil {
		return "", err
	}

	ts := o.restQueryServerTimestamp()
	sign := o.buildLoginSign(ts, constants.HttpMethodPost, constants.OkexDexBroadcastTransaction, string(bodyBytes), record.SecretKey)

	baseURL := strings.TrimRight(constants.OkexDexBaseUrl, "/")
	fullURL := fmt.Sprintf("%s/%s", baseURL, strings.TrimLeft(constants.OkexDexBroadcastTransaction, "/"))

	resp, err := o.restClient.DoPost(constants.ConnectTypeOKEX, fullURL, string(bodyBytes), record.AppKey, sign, record.Passphrase, ts)
	if err != nil {
		log.Errorf("broadcastTransaction - OKEx broadcast request failed: %v", err)
		return "", fmt.Errorf("broadcast transaction request failed: %w", err)
	}

	var broadcastResp model.OkexBroadcastTxResponse
	if err := json.Unmarshal([]byte(resp), &broadcastResp); err != nil {
		log.Errorf("broadcastTransaction - OKEx broadcast unmarshal failed: %v, response: %s", err, resp)
		return "", fmt.Errorf("broadcast transaction unmarshal failed: %w", err)
	}

	if broadcastResp.Code != "" && broadcastResp.Code != "0" {
		log.Errorf("broadcastTransaction - OKEx broadcast failed: code=%s, msg=%s", broadcastResp.Code, broadcastResp.Msg)
		return "", fmt.Errorf("broadcast failed: code=%s, msg=%s", broadcastResp.Code, broadcastResp.Msg)
	}

	if len(broadcastResp.Data) == 0 {
		log.Errorf("broadcastTransaction - OKEx broadcast returned no data")
		return "", fmt.Errorf("no broadcast data returned")
	}

	return broadcastResp.Data[0].TxHash, nil
}

// broadcastTransactionDirect 直接通过 OKEx API 广播交易（不使用 bundle）
// 用于授权交易等不需要走 bundle 的场景
func (o *okdex) broadcastTransactionDirect(signedTx, chainIndex, walletAddress string, index int, actualTxHash string) (string, error) {
	log := logger.GetLoggerInstance().Named("okdex").Sugar()

	reqBody := model.OkexBroadcastTxRequest{
		SignedTx:   signedTx,
		ChainIndex: chainIndex,
		Address:    walletAddress,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal body failed: %v", err)
	}

	record, err := o.getAppKeyByIndex(index)
	if err != nil {
		return "", err
	}

	ts := o.restQueryServerTimestamp()
	sign := o.buildLoginSign(ts, constants.HttpMethodPost, constants.OkexDexBroadcastTransaction, string(bodyBytes), record.SecretKey)

	baseURL := strings.TrimRight(constants.OkexDexBaseUrl, "/")
	fullURL := fmt.Sprintf("%s/%s", baseURL, strings.TrimLeft(constants.OkexDexBroadcastTransaction, "/"))

	resp, err := o.restClient.DoPost(constants.ConnectTypeOKEX, fullURL, string(bodyBytes), record.AppKey, sign, record.Passphrase, ts)
	if err != nil {
		log.Errorf("broadcastTransactionDirect - OKEx broadcast request failed: %v", err)
		return "", fmt.Errorf("broadcast transaction request failed: %w", err)
	}

	var broadcastResp model.OkexBroadcastTxResponse
	if err := json.Unmarshal([]byte(resp), &broadcastResp); err != nil {
		log.Errorf("broadcastTransactionDirect - OKEx broadcast unmarshal failed: %v, response: %s", err, resp)
		return "", fmt.Errorf("broadcast transaction unmarshal failed: %w", err)
	}

	if broadcastResp.Code != "" && broadcastResp.Code != "0" {
		log.Errorf("broadcastTransactionDirect - OKEx broadcast failed: code=%s, msg=%s", broadcastResp.Code, broadcastResp.Msg)
		return "", fmt.Errorf("broadcast failed: code=%s, msg=%s", broadcastResp.Code, broadcastResp.Msg)
	}

	if len(broadcastResp.Data) == 0 {
		log.Errorf("broadcastTransactionDirect - OKEx broadcast returned no data")
		return "", fmt.Errorf("no broadcast data returned")
	}

	log.Infof("broadcastTransactionDirect - 授权交易广播成功 (不走 bundle): txHash=%s", broadcastResp.Data[0].TxHash)
	return broadcastResp.Data[0].TxHash, nil
}

// queryTxResult 查询广播交易的执行状态
func (o *okdex) queryTxResult(txHash, chainIndex string) (model.TradeResult, error) {
	log := logger.GetLoggerInstance().Named("okdex").Sugar()
	result := model.TradeResult{Status: constants.TradeStatusInit}

	if txHash == "" || chainIndex == "" {
		return result, fmt.Errorf("invalid params")
	}

	values := url.Values{}
	values.Add("chainIndex", chainIndex)
	values.Add("txHash", txHash)

	resp, err := o.doAPIRequest(constants.HttpMethodGet, constants.OkexDexPostTransactionOrders, values, "")
	if err != nil {
		log.Errorf("queryTxResult - API request failed - txHash: %s, chainIndex: %s, error: %v", txHash, chainIndex, err)
		return result, err
	}

	var txStatusResp model.OkexTxStatusResponse
	if err := json.Unmarshal([]byte(resp), &txStatusResp); err != nil {
		log.Errorf("queryTxResult - unmarshal response failed - txHash: %s, chainIndex: %s, error: %v, response: %s",
			txHash, chainIndex, err, resp)
		return result, err
	}

	if txStatusResp.Code != "0" {
		log.Errorf("queryTxResult - API error - txHash: %s, chainIndex: %s, code: %s, msg: %s",
			txHash, chainIndex, txStatusResp.Code, txStatusResp.Msg)
		return result, fmt.Errorf("okex status api error: %s", txStatusResp.Msg)
	}

	txData := txStatusResp.Data

	// 如果 status 为空字符串，可能是交易还未上链或被 bundler 处理中，视为 pending
	if txData.Status == "" {
		result.Status = constants.TradeStatusSwapping
		return result, fmt.Errorf("pending")
	}

	// 状态字段是小写的 status
	switch txData.Status {
	case "pending":
		result.Status = constants.TradeStatusSwapping
		return result, fmt.Errorf("pending")
	case "success":
		result.Status = constants.TradeStatusSuccess
		result.AmountIn = txData.FromTokenDetails.Amount
		result.AmountOut = txData.ToTokenDetails.Amount
		result.GasUsed = txData.GasUsed
		result.TxFee = txData.TxFee
		// 当 API 未返回 txFee（如 EVM 链部分版本不返回）时，用 gasUsed * gasPrice 计算（wei），下游会按 >1e10 做 /1e18 换算
		if result.TxFee == "" && txData.GasUsed != "" && txData.GasPrice != "" {
			if computed := computeTxFeeFromGas(txData.GasUsed, txData.GasPrice); computed != "" {
				result.TxFee = computed
				log.Debugf("queryTxResult - txFee 由 gasUsed*gasPrice 计算 | txHash=%s, gasUsed=%s, gasPrice=%s -> txFee=%s", txHash, txData.GasUsed, txData.GasPrice, result.TxFee)
			}
		}
		if result.AmountIn == "" || result.AmountOut == "" {
			log.Warnf("queryTxResult - 确认成功但数量为空 | txHash=%s, fromToken.amount=%s, fromToken.symbol=%s, toToken.amount=%s, toToken.symbol=%s",
				txHash, txData.FromTokenDetails.Amount, txData.FromTokenDetails.Symbol, txData.ToTokenDetails.Amount, txData.ToTokenDetails.Symbol)
		} else {
			log.Debugf("queryTxResult - 确认成功 | txHash=%s, AmountIn=%s, AmountOut=%s, TxFee=%s", txHash, result.AmountIn, result.AmountOut, result.TxFee)
		}
		return result, nil
	case "fail":
		result.Status = constants.TradeStatusFailed
		result.ErrorMsg = txData.ErrorMsg
		log.Errorf("queryTxResult - transaction failed - txHash: %s, errorMsg: %s", txHash, txData.ErrorMsg)
		return result, errors.New(result.ErrorMsg)
	default:
		// 对于未知状态，也视为 pending（可能是交易还未被确认）
		result.Status = constants.TradeStatusSwapping
		return result, fmt.Errorf("pending")
	}
}

// computeTxFeeFromGas 用 gasUsed * gasPrice 计算交易费（wei），返回十进制字符串；解析失败返回空字符串（base 0 支持十进制与 0x 十六进制）
func computeTxFeeFromGas(gasUsed, gasPrice string) string {
	u := new(big.Int)
	if _, ok := u.SetString(gasUsed, 0); !ok {
		return ""
	}
	p := new(big.Int)
	if _, ok := p.SetString(gasPrice, 0); !ok {
		return ""
	}
	fee := new(big.Int).Mul(u, p)
	return fee.String()
}
