package onchain

import (
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/onchain/bridge"
	"github.com/qw225967/auto-monitor/internal/onchain/bundler"
	"github.com/qw225967/auto-monitor/internal/utils/rest"
	"fmt"
	"strconv"
	"sync"
)

var _ OnchainClient = (*okdex)(nil)

type okdex struct {
	mu            sync.RWMutex
	priceCallback PriceCallback
	isInitialized bool

	swapInfo         *model.SwapInfo
	latestBuySwapTx  interface{}
	latestSellSwapTx interface{}
	swapRunning      bool

	// 缓存最新的交易详情（用于直接广播，避免查找）
	latestBuyTxDetail  *model.OkexDexTx
	latestSellTxDetail *model.OkexDexTx

	nonceCache map[string]*nonceCacheItem
	nonceMu    sync.RWMutex

	restClient rest.RestClient

	bundlerManager *bundler.Manager
	useBundler     bool

	bridgeManager *bridge.Manager
}

type nonceCacheItem struct {
	nonce   uint64
	isValid bool
}

// NewOkdex 创建 OKEx DEX 客户端实例（Gas 配置从全局配置获取）
func NewOkdex() OnchainClient {
	return &okdex{
		isInitialized:    false,
		swapInfo:         nil,
		latestBuySwapTx:  nil,
		latestSellSwapTx: nil,
		swapRunning:      false,
		nonceCache:       make(map[string]*nonceCacheItem),
		bundlerManager:   nil,
		useBundler:       false,
		bridgeManager:    nil,
	}
}

// getGasMultiplier 获取 Gas 乘数（总是从全局配置读取最新值）
func (o *okdex) getGasMultiplier() float64 {
	globalConfig := config.GetGlobalConfig()
	if globalConfig != nil && globalConfig.Onchain.GasMultiplier > 0 {
		return globalConfig.Onchain.GasMultiplier
	}
	return 1.0 // 默认不调整
}

// SetBundlerManager 设置 bundler 管理器
func (o *okdex) SetBundlerManager(manager *bundler.Manager, useBundler bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.bundlerManager = manager
	o.useBundler = useBundler
}

// SetBundlerForClient 为 OnchainClient 设置 bundler
func SetBundlerForClient(client OnchainClient, manager *bundler.Manager, useBundler bool) {
	if okdexClient, ok := client.(*okdex); ok {
		okdexClient.SetBundlerManager(manager, useBundler)
	}
}

func (o *okdex) EnableBundler() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.useBundler = true
}

func (o *okdex) DisableBundler() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.useBundler = false
}

// SetGasMultiplier 设置 gas 乘数（已废弃，现在从全局配置读取）
// 保留此方法以保持接口兼容性，但实际值从全局配置读取
func (o *okdex) SetGasMultiplier(multiplier float64) {
	// 不再存储，现在从全局配置读取
	// 此方法保留以保持接口兼容性
}

// GetGasMultiplier 获取 gas 乘数（总是从全局配置读取最新值）
func (o *okdex) GetGasMultiplier() float64 {
	return o.getGasMultiplier()
}

func (o *okdex) IsBundlerEnabled() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.useBundler
}

// IsBundlerEnabledForClient 查询 OnchainClient 是否开启 bundler
func IsBundlerEnabledForClient(client OnchainClient) bool {
	if okdexClient, ok := client.(*okdex); ok {
		return okdexClient.IsBundlerEnabled()
	}
	return false
}

// DisableBundlerForClient 为 OnchainClient 关闭 bundler
func DisableBundlerForClient(client OnchainClient) {
	if okdexClient, ok := client.(*okdex); ok {
		okdexClient.DisableBundler()
	}
}

// EnableBundlerForClient 为 OnchainClient 开启 bundler
func EnableBundlerForClient(client OnchainClient) {
	if okdexClient, ok := client.(*okdex); ok {
		okdexClient.EnableBundler()
	}
}

// Init 初始化连接
func (o *okdex) Init() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.isInitialized {
		return nil
	}

	o.restClient.InitRestClient()

	manager := config.GetOkexKeyManager()
	if err := manager.Init(); err != nil {
		return fmt.Errorf("failed to init OKEx key manager: %w", err)
	}

	o.isInitialized = true
	return nil
}

// SetPriceCallback 设置价格数据回调函数
func (o *okdex) SetPriceCallback(callback PriceCallback) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.priceCallback = callback
}

// StartSwap 开始循环交易
func (o *okdex) StartSwap(swapInfo *model.SwapInfo) {
	if swapInfo == nil {
		return
	}

	if !o.isInitialized {
		return
	}

	_ = o.approveToken(swapInfo.FromTokenContractAddress, swapInfo.ChainIndex, swapInfo.WalletAddress)
	_ = o.approveToken(swapInfo.ToTokenContractAddress, swapInfo.ChainIndex, swapInfo.WalletAddress)

	o.swapInfo = swapInfo

	if !o.swapRunning {
		o.swapRunning = true
		go o.processSwapPriceQueriesLoop()
	}
}

// StopSwap 停止链上询价循环（trigger 删除时调用，避免 goroutine 常驻）
func (o *okdex) StopSwap() {
	o.mu.Lock()
	o.swapRunning = false
	o.mu.Unlock()
}

// BroadcastSwapTx 广播交易（直接使用缓存的 TxDetail）
func (o *okdex) BroadcastSwapTx(direction SwapDirection) (string, error) {
	o.mu.RLock()
	initialized := o.isInitialized
	swap := o.swapInfo
	var txDetail *model.OkexDexTx
	if direction == SwapDirectionBuy {
		txDetail = o.latestBuyTxDetail
	} else {
		txDetail = o.latestSellTxDetail
	}
	o.mu.RUnlock()

	if !initialized {
		return "", ErrNotInitialized
	}

	if swap == nil {
		return "", fmt.Errorf("swap info not set")
	}

	if txDetail == nil {
		return "", fmt.Errorf("latest %s tx detail not available", direction)
	}

	// 调整 Gas Limit（如果使用 bundler，增加 30%）
	o.mu.RLock()
	useBundler := o.useBundler && o.bundlerManager != nil
	o.mu.RUnlock()

	gasLimit := o.adjustGasLimit(txDetail.Gas, useBundler)

	// 构造交易详情（使用 swap 查询返回的参数）
	swapTxDetail := model.OkexSwapTxDetail{
		GasPrice: txDetail.GasPrice,
		Gas:      gasLimit,
		Data:     txDetail.Data,
		To:       txDetail.To,
		Value:    txDetail.Value,
	}

	// 签名交易（使用最新的 nonce）
	signedTx, err := o.signEIP1559Tx(swap.WalletAddress, swap.ChainIndex, swapTxDetail)
	if err != nil {
		o.resetNonceCache(swap.WalletAddress, swap.ChainIndex)
		return "", fmt.Errorf("sign transaction failed: %w", err)
	}

	// 提取交易哈希
	actualTxHash := o.extractTxHashFromSignedTx(signedTx)

	// 获取 API Key
	record, err := o.getNextAppKey(true)
	if err != nil {
		return "", err
	}

	// 广播交易
	txHash, err := o.broadcastTransaction(signedTx, swap.ChainIndex, swap.WalletAddress, record.Index, actualTxHash)
	if err != nil {
		o.resetNonceCache(swap.WalletAddress, swap.ChainIndex)
		return "", err
	}

	return txHash, nil
}

// adjustGasLimit 调整 gas limit
// 1. 先应用 gasMultiplier（用户配置的乘数）
// 2. 如果使用 bundler，再额外增加 30%
func (o *okdex) adjustGasLimit(originalGasLimit string, useBundler bool) string {
	gasLimitInt, err := strconv.ParseInt(originalGasLimit, 10, 64)
	if err != nil {
		return originalGasLimit
	}

	// 1. 应用 gas 乘数（从全局配置获取）
	multiplier := o.getGasMultiplier()

	if multiplier > 1.0 {
		gasLimitInt = int64(float64(gasLimitInt) * multiplier)
	}

	// 2. bundler 额外增加 30%
	if useBundler {
		gasLimitInt = gasLimitInt + (gasLimitInt * 30 / 100)
	}

	return strconv.FormatInt(gasLimitInt, 10)
}

// GetTxResult 查询交易是否完成
func (o *okdex) GetTxResult(txHash, chainIndex string) (model.TradeResult, error) {
	o.mu.RLock()
	initialized := o.isInitialized
	o.mu.RUnlock()
	if !initialized {
		return model.TradeResult{}, ErrNotInitialized
	}
	return o.queryTxResult(txHash, chainIndex)
}

// GetBalance 查询链上代币余额（单个代币）
func (o *okdex) GetBalance() (*model.TokenBalance, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if !o.isInitialized {
		return nil, ErrNotInitialized
	}

	return &model.TokenBalance{}, nil
}

// GetAllTokenBalances 查询地址持有的多个链或指定链的代币余额列表
func (o *okdex) GetAllTokenBalances(address, chains string, excludeRiskToken bool) ([]model.OkexTokenAsset, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if !o.isInitialized {
		return nil, ErrNotInitialized
	}

	excludeRiskTokenStr := "0"
	if !excludeRiskToken {
		excludeRiskTokenStr = "1"
	}

	balanceResponse, err := o.queryAllTokenBalances(address, chains, excludeRiskTokenStr)
	if err != nil {
		return nil, fmt.Errorf("failed to query token balances: %w", err)
	}

	var allAssets []model.OkexTokenAsset
	for _, data := range balanceResponse.Data {
		allAssets = append(allAssets, data.TokenAssets...)
	}

	return allAssets, nil
}

// GetLatestSwapTx 获取最新的 Swap 交易数据
func (o *okdex) GetLatestSwapTx() interface{} {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.latestSellSwapTx != nil {
		return o.latestSellSwapTx
	}
	return o.latestBuySwapTx
}

// GetSwapInfo 获取当前的 Swap 信息
func (o *okdex) GetSwapInfo() *model.SwapInfo {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.swapInfo
}

// UpdateSwapInfoAmount 更新 Swap 信息中的 Amount 字段
func (o *okdex) UpdateSwapInfoAmount(amount string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.swapInfo != nil {
		o.swapInfo.Amount = amount
	}
}

// UpdateSwapInfoDecimals 更新 Swap 信息中的 Decimals 字段
func (o *okdex) UpdateSwapInfoDecimals(decimalsFrom, decimalsTo string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.swapInfo != nil {
		if decimalsFrom != "" {
			o.swapInfo.DecimalsFrom = decimalsFrom
		}
		if decimalsTo != "" {
			o.swapInfo.DecimalsTo = decimalsTo
		}
	}
}

// UpdateSwapInfoSlippage 更新 Swap 信息中的 Slippage 字段
func (o *okdex) UpdateSwapInfoSlippage(slippage string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.swapInfo != nil {
		o.swapInfo.Slippage = slippage
	}
}

// SetSwapInfo 设置 Swap 信息
func (o *okdex) SetSwapInfo(swapInfo *model.SwapInfo) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.swapInfo = swapInfo
}

// UpdateSwapInfoGasLimit 更新 Swap 信息中的 GasLimit 字段
func (o *okdex) UpdateSwapInfoGasLimit(gasLimit string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.swapInfo != nil {
		o.swapInfo.GasLimit = gasLimit
	}
}

// ResetNonce 重置 nonce 缓存
func (o *okdex) ResetNonce(walletAddress, chainIndex string) {
	o.resetNonceCache(walletAddress, chainIndex)
}

// SetBridgeManager 设置跨链协议管理器
func (o *okdex) SetBridgeManager(manager *bridge.Manager) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.bridgeManager = manager
}

// BridgeToken 跨链转账
func (o *okdex) BridgeToken(req *model.BridgeRequest) (*model.BridgeResponse, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if !o.isInitialized {
		return nil, ErrNotInitialized
	}

	if o.bridgeManager == nil {
		return nil, fmt.Errorf("bridge manager not initialized")
	}

	return o.bridgeManager.BridgeToken(req)
}

// GetBridgeStatus 查询跨链状态
func (o *okdex) GetBridgeStatus(txHash string, fromChain, toChain string) (*model.BridgeStatus, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if !o.isInitialized {
		return nil, ErrNotInitialized
	}

	if o.bridgeManager == nil {
		return nil, fmt.Errorf("bridge manager not initialized")
	}

	// 尝试从所有协议查询状态
	return o.bridgeManager.GetBridgeStatus(txHash, fromChain, toChain, "")
}

// GetBridgeQuote 获取跨链报价
func (o *okdex) GetBridgeQuote(req *model.BridgeQuoteRequest) (*model.BridgeQuote, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if !o.isInitialized {
		return nil, ErrNotInitialized
	}

	if o.bridgeManager == nil {
		return nil, fmt.Errorf("bridge manager not initialized")
	}

	return o.bridgeManager.GetBridgeQuote(req)
}
