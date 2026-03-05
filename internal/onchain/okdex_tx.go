package onchain

import (
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
	"fmt"
	"strconv"

	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// queryTransactionNonce 获取交易随机数（带缓存和自增）
func (o *okdex) queryTransactionNonce(walletAddress, chainIndex string) (string, error) {
	log := logger.GetLoggerInstance().Named("okdex.nonce").Sugar()
	cacheKey := fmt.Sprintf("%s:%s", walletAddress, chainIndex)

	o.nonceMu.RLock()
	cached, exists := o.nonceCache[cacheKey]
	o.nonceMu.RUnlock()

	if exists && cached.isValid {
		o.nonceMu.Lock()
		currentNonce := cached.nonce
		cached.nonce++
		o.nonceMu.Unlock()
		return strconv.FormatUint(currentNonce, 10), nil
	}

	nonceStr, err := o.fetchNonceFromServer(walletAddress, chainIndex)
	if err != nil {
		log.Errorf("queryTransactionNonce - fetch nonce failed - walletAddress: %s, chainIndex: %s, error: %v",
			walletAddress, chainIndex, err)
		return "", err
	}

	nonceInt64, err := strconv.ParseInt(nonceStr, 10, 64)
	if err != nil {
		log.Errorf("queryTransactionNonce - invalid nonce from server - walletAddress: %s, chainIndex: %s, nonceStr: %s, error: %v",
			walletAddress, chainIndex, nonceStr, err)
		return "", fmt.Errorf("invalid nonce from server: %w", err)
	}
	nonce := uint64(nonceInt64)

	o.nonceMu.Lock()
	o.nonceCache[cacheKey] = &nonceCacheItem{
		nonce:   nonce + 1,
		isValid: true,
	}
	o.nonceMu.Unlock()

	return nonceStr, nil
}

// resetNonceCache 重置 nonce 缓存
func (o *okdex) resetNonceCache(walletAddress, chainIndex string) {
	log := logger.GetLoggerInstance().Named("okdex.nonce").Sugar()
	cacheKey := fmt.Sprintf("%s:%s", walletAddress, chainIndex)
	o.nonceMu.Lock()
	defer o.nonceMu.Unlock()

	if cached, exists := o.nonceCache[cacheKey]; exists {
		oldNonce := cached.nonce
		cached.isValid = false
		log.Warnf("resetNonceCache - nonce cache reset - walletAddress: %s, chainIndex: %s, oldNonce: %d",
			walletAddress, chainIndex, oldNonce)
	}
}

// signEIP1559Tx 签名EIP-1559交易
func (o *okdex) signEIP1559Tx(walletAddress, chainIndex string, swapTx model.OkexSwapTxDetail) (string, error) {
	log := logger.GetLoggerInstance().Named("okdex.tx").Sugar()

	nonceStr, err := o.queryTransactionNonce(walletAddress, chainIndex)
	if err != nil {
		log.Errorf("signEIP1559Tx - get nonce failed - walletAddress: %s, chainIndex: %s, error: %v",
			walletAddress, chainIndex, err)
		return "", fmt.Errorf("get nonce failed: %w", err)
	}
	nonceInt64, err := strconv.ParseInt(nonceStr, 10, 64)
	if err != nil {
		log.Errorf("signEIP1559Tx - invalid nonce format - walletAddress: %s, chainIndex: %s, nonceStr: %s, error: %v",
			walletAddress, chainIndex, nonceStr, err)
		return "", fmt.Errorf("invalid nonce: %w", err)
	}
	nonce := uint64(nonceInt64)

	toAddress := common.HexToAddress(swapTx.To)
	dataBytes := common.FromHex(swapTx.Data)

	valueInt := new(big.Int)
	if swapTx.Value != "" {
		valueInt.SetString(swapTx.Value, 10)
	}

	gasInt, err := strconv.ParseInt(swapTx.Gas, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid gas: %w", err)
	}

	gasPriceInt := new(big.Int)
	gasPriceInt.SetString(swapTx.GasPrice, 10)
	gasTipCapInt := new(big.Int).Set(gasPriceInt)
	gasFeeCap := new(big.Int).Mul(gasPriceInt, big.NewInt(3))

	chainID, err := strconv.ParseInt(chainIndex, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid chain index: %w", err)
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(chainID),
		Nonce:     nonce,
		GasTipCap: gasTipCapInt,
		GasFeeCap: gasFeeCap,
		Gas:       uint64(gasInt),
		To:        &toAddress,
		Value:     valueInt,
		Data:      dataBytes,
	})

	if config.GetGlobalConfig().Wallet.PrivateSecret == "" {
		return "", fmt.Errorf("private key not configured")
	}
	privateKey, err := crypto.HexToECDSA(strings.TrimPrefix(config.GetGlobalConfig().Wallet.PrivateSecret, "0x"))
	if err != nil {
		return "", fmt.Errorf("invalid private key: %w", err)
	}

	signer := types.LatestSignerForChainID(tx.ChainId())
	signedTx, err := types.SignTx(tx, signer, privateKey)
	if err != nil {
		return "", fmt.Errorf("sign tx failed: %w", err)
	}

	signedTxBytes, err := signedTx.MarshalBinary()
	if err != nil {
		log.Errorf("signEIP1559Tx - marshal tx failed - walletAddress: %s, chainIndex: %s, nonce: %d, error: %v",
			walletAddress, chainIndex, nonce, err)
		return "", fmt.Errorf("marshal tx failed: %w", err)
	}

	return "0x" + common.Bytes2Hex(signedTxBytes), nil
}

// extractTxHashFromSignedTx 从签名交易字节中提取交易 hash
func (o *okdex) extractTxHashFromSignedTx(signedTxHex string) string {
	if signedTxHex == "" {
		return ""
	}

	// 移除 "0x" 前缀
	txBytes := common.FromHex(signedTxHex)
	if len(txBytes) == 0 {
		return ""
	}

	// 解析交易
	var tx types.Transaction
	if err := tx.UnmarshalBinary(txBytes); err != nil {
		return ""
	}

	// 返回交易 hash
	return tx.Hash().Hex()
}

// signApproveTransaction 签名授权交易
func (o *okdex) signApproveTransaction(walletAddress, chainIndex string, approveData model.OkexDexApproveTransactionData) (string, error) {
	swapTx := model.OkexSwapTxDetail{
		GasPrice: approveData.GasPrice,
		Gas:      approveData.GasLimit,
		Data:     approveData.Data,
		To:       approveData.To,
		Value:    "0",
	}

	if swapTx.To == "" {
		return "", fmt.Errorf("approve transaction To address is empty")
	}

	return o.signEIP1559Tx(walletAddress, chainIndex, swapTx)
}

// approveTokenWithHash 对代币进行授权，返回交易哈希
// 注意：授权交易不走 bundle，直接通过 OKEx API 广播
func (o *okdex) approveTokenWithHash(tokenAddress, chainIndex, walletAddress string) (string, error) {
	if tokenAddress == "" || chainIndex == "" || walletAddress == "" {
		return "", fmt.Errorf("invalid approve parameters")
	}

	approveResp, err := o.queryApproveTransaction(tokenAddress, chainIndex, walletAddress)
	if err != nil {
		return "", fmt.Errorf("query approve transaction failed: %w", err)
	}

	if len(approveResp.Data) == 0 {
		return "", nil // 已经授权，返回空哈希
	}

	approveResp.Data[0].To = tokenAddress

	signedTx, err := o.signApproveTransaction(walletAddress, chainIndex, approveResp.Data[0])
	if err != nil {
		o.resetNonceCache(walletAddress, chainIndex)
		return "", fmt.Errorf("sign approve transaction failed: %w", err)
	}

	record, err := o.getNextAppKey(true)
	if err != nil {
		o.resetNonceCache(walletAddress, chainIndex)
		return "", fmt.Errorf("get broadcastable key failed: %w", err)
	}

	// 从签名交易中提取交易 hash
	actualTxHash := o.extractTxHashFromSignedTx(signedTx)
	// 授权交易不走 bundle，直接通过 OKEx API 广播
	txHash, err := o.broadcastTransactionDirect(signedTx, chainIndex, walletAddress, record.Index, actualTxHash)
	if err != nil {
		o.resetNonceCache(walletAddress, chainIndex)
		return "", fmt.Errorf("broadcast approve transaction failed: %w", err)
	}

	return txHash, nil
}

// approveToken 对代币进行授权（兼容旧接口）
func (o *okdex) approveToken(tokenAddress, chainIndex, walletAddress string) error {
	_, err := o.approveTokenWithHash(tokenAddress, chainIndex, walletAddress)
	return err
}

