package wormhole

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/onchain/bridge"
	"auto-arbitrage/internal/utils/logger"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// 确保 *Wormhole 实现 bridge.BridgeProtocol
var _ bridge.BridgeProtocol = (*Wormhole)(nil)

const (
	// defaultWormholeFeeWei Portal 协议费兜底（约 0.0001 原生币），实际由 Core 合约的 messageFee() 决定
	defaultWormholeFeeWei = 100_000_000_000_000 // 0.0001 * 1e18
	wormholescanAPIBase   = "https://api.wormholescan.io"
)

//go:embed token_bridge_abi.json
var tokenBridgeABIFS embed.FS

// Wormhole 实现 Wormhole 跨链协议
type Wormhole struct {
	rpcURLs map[string]string // RPC URLs for different chains
	enabled bool

	// 代币合约地址映射：key 为 "chainID:symbol"，value 为合约地址
	tokenContracts map[string]string

	// 用于存储跨链状态（初始/跟踪用，真实状态通过 GetBridgeStatus 查链上）
	bridgeStatuses map[string]*model.BridgeStatus
	mu             sync.RWMutex

	// ethclient 连接缓存：key 为 chainID
	clients   map[string]*ethclient.Client
	clientsMu sync.RWMutex

	// Token Bridge ABI（缓存）
	tokenBridgeABI     *abi.ABI
	tokenBridgeABIOnce sync.Once

	// receiptFetcher 仅用于单测注入
	receiptFetcher func(ctx context.Context, chainID string, txHash common.Hash) (*types.Receipt, error)
}

// SetReceiptFetcherForTest 供单测注入
func (w *Wormhole) SetReceiptFetcherForTest(fn func(ctx context.Context, chainID string, txHash common.Hash) (*types.Receipt, error)) {
	w.receiptFetcher = fn
}

// NewWormhole 创建 Wormhole 协议实例
func NewWormhole(rpcURLs map[string]string, enabled bool) *Wormhole {
	if rpcURLs == nil {
		rpcURLs = make(map[string]string)
	}
	return &Wormhole{
		rpcURLs:        rpcURLs,
		enabled:        enabled,
		tokenContracts: make(map[string]string),
		bridgeStatuses: make(map[string]*model.BridgeStatus),
		clients:        make(map[string]*ethclient.Client),
	}
}

// SetRPCURL 设置指定链的 RPC URL
func (w *Wormhole) SetRPCURL(chainID, rpcURL string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.rpcURLs == nil {
		w.rpcURLs = make(map[string]string)
	}
	w.rpcURLs[chainID] = rpcURL
}

// SetTokenContract 设置指定链和代币的合约地址
func (w *Wormhole) SetTokenContract(chainID, tokenSymbol, contractAddress string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.tokenContracts == nil {
		w.tokenContracts = make(map[string]string)
	}
	w.tokenContracts[bridge.OFTTokenKey(chainID, tokenSymbol)] = contractAddress
}

// GetTokenContract 获取指定链和代币的合约地址
func (w *Wormhole) GetTokenContract(chainID, tokenSymbol string) (string, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	addr, ok := w.tokenContracts[bridge.OFTTokenKey(chainID, tokenSymbol)]
	return addr, ok && addr != ""
}

// getEthClient 获取指定链的 ethclient，带缓存和 RPC 回退
func (w *Wormhole) getEthClient(chainID string) (*ethclient.Client, error) {
	w.clientsMu.RLock()
	if client, ok := w.clients[chainID]; ok && client != nil {
		w.clientsMu.RUnlock()
		return client, nil
	}
	w.clientsMu.RUnlock()

	var rpcURLs []string
	if rpcURL, ok := w.rpcURLs[chainID]; ok && rpcURL != "" {
		rpcURLs = append(rpcURLs, rpcURL)
	}
	backupURLs := constants.GetDefaultRPCURLs(chainID)
	for _, url := range backupURLs {
		found := false
		for _, existing := range rpcURLs {
			if existing == url {
				found = true
				break
			}
		}
		if !found {
			rpcURLs = append(rpcURLs, url)
		}
	}

	if len(rpcURLs) == 0 {
		return nil, fmt.Errorf("RPC URL not configured for chain %s", chainID)
	}

	var lastErr error
	for _, rpcURL := range rpcURLs {
		client, err := ethclient.Dial(rpcURL)
		if err == nil {
			_, err = client.ChainID(context.Background())
			if err == nil {
				w.clientsMu.Lock()
				w.clients[chainID] = client
				w.clientsMu.Unlock()
				logger.GetLoggerInstance().Named("bridge.wormhole").Sugar().Infof("Connected to RPC for chain %s", chainID)
				return client, nil
			}
			client.Close()
		}
		lastErr = err
		logger.GetLoggerInstance().Named("bridge.wormhole").Sugar().Warnf("Failed to connect to RPC for chain %s: %v", chainID, err)
	}

	return nil, fmt.Errorf("failed to connect to any RPC for chain %s: %w", chainID, lastErr)
}

// getTokenBridgeABI 获取 Token Bridge ABI
func (w *Wormhole) getTokenBridgeABI() (*abi.ABI, error) {
	var err error
	w.tokenBridgeABIOnce.Do(func() {
		abiData, readErr := tokenBridgeABIFS.ReadFile("token_bridge_abi.json")
		if readErr != nil {
			err = fmt.Errorf("failed to read Token Bridge ABI: %w", readErr)
			return
		}
		var parsedABI abi.ABI
		if parseErr := json.Unmarshal(abiData, &parsedABI); parseErr != nil {
			err = fmt.Errorf("failed to parse Token Bridge ABI: %w", parseErr)
			return
		}
		w.tokenBridgeABI = &parsedABI
	})
	if err != nil {
		return nil, err
	}
	if w.tokenBridgeABI == nil {
		return nil, fmt.Errorf("Token Bridge ABI not loaded")
	}
	return w.tokenBridgeABI, nil
}

// getTokenDecimals 查询 ERC20 的 decimals
func (w *Wormhole) getTokenDecimals(ctx context.Context, client *ethclient.Client, tokenContract common.Address) (int, error) {
	decimalsCalldata := common.Hex2Bytes("313ce567")
	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &tokenContract,
		Data: decimalsCalldata,
	}, nil)
	if err != nil {
		return 0, fmt.Errorf("call decimals failed: %w", err)
	}
	if len(result) < 32 {
		return 0, fmt.Errorf("decimals() returned invalid length: %d", len(result))
	}
	d := int(result[31])
	if d < 0 || d > 77 {
		return 0, fmt.Errorf("decimals out of range: %d", d)
	}
	return d, nil
}

// amountToAmountLD 将人类可读数量转为代币最小单位
func amountToAmountLD(amountFloat float64, decimals int) *big.Int {
	multiplier := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	mult := new(big.Float).SetPrec(256).SetInt(multiplier)
	amountF := new(big.Float).SetPrec(256).SetFloat64(amountFloat)
	amountF.Mul(amountF, mult)
	var amountLD big.Int
	amountF.Int(&amountLD)
	return &amountLD
}

func parseAmount(amountStr string) (float64, error) {
	amountStr = strings.TrimSpace(amountStr)
	if amountStr == "" {
		return 0, fmt.Errorf("empty amount")
	}
	amount, err := strconv.ParseFloat(amountStr, 64)
	if err == nil {
		return amount, nil
	}
	return 0, fmt.Errorf("invalid amount format: %s", amountStr)
}

// GetName 获取协议名称
func (w *Wormhole) GetName() string {
	return "wormhole"
}

// CheckBridgeReady 预检查 Wormhole 跨链条件
func (w *Wormhole) CheckBridgeReady(fromChain, toChain, tokenSymbol string) error {
	if !w.enabled {
		return fmt.Errorf("wormhole bridge is not enabled")
	}
	if !w.IsChainPairSupported(fromChain, toChain) {
		return fmt.Errorf("wormhole does not support chain pair %s -> %s", fromChain, toChain)
	}
	_, fromOK := w.GetTokenContract(fromChain, tokenSymbol)
	_, toOK := w.GetTokenContract(toChain, tokenSymbol)
	if !fromOK || !toOK {
		return fmt.Errorf("Wormhole token contract not found for %s on fromChain or toChain. "+
			"Please configure in config: bridge.wormhole.tokenContracts (e.g. \"%s\" = \"<address>\")",
			tokenSymbol, bridge.OFTTokenKey(fromChain, tokenSymbol))
	}
	// 检查 RPC 是否可用
	if _, err := w.getEthClient(fromChain); err != nil {
		return fmt.Errorf("cannot connect to RPC for fromChain %s: %w", fromChain, err)
	}
	if _, err := w.getEthClient(toChain); err != nil {
		return fmt.Errorf("cannot connect to RPC for toChain %s: %w", toChain, err)
	}
	return nil
}

// BridgeToken 执行跨链转账
func (w *Wormhole) BridgeToken(req *model.BridgeRequest) (*model.BridgeResponse, error) {
	log := logger.GetLoggerInstance().Named("bridge.wormhole").Sugar()

	if !w.enabled {
		return nil, fmt.Errorf("wormhole bridge is not enabled")
	}
	if !w.IsChainPairSupported(req.FromChain, req.ToChain) {
		return nil, fmt.Errorf("wormhole does not support chain pair %s -> %s", req.FromChain, req.ToChain)
	}

	tokenAddr, ok := w.GetTokenContract(req.FromChain, req.FromToken)
	if !ok || tokenAddr == "" {
		return nil, fmt.Errorf("token contract not configured for %s on chain %s. Add in config: bridge.wormhole.tokenContracts[\"%s:%s\"]",
			req.FromToken, req.FromChain, req.FromChain, req.FromToken)
	}

	txHash, estimatedFee, err := w.executeRealBridge(req, tokenAddr)
	if err != nil {
		return nil, fmt.Errorf("wormhole bridge failed: %w", err)
	}

	n := 16
	if len(txHash) < n {
		n = len(txHash)
	}
	bridgeID := fmt.Sprintf("wh_%s_%d", txHash[:n], time.Now().Unix())
	feeStr := "0"
	if estimatedFee != nil {
		feeStr = estimatedFee.String()
	}
	log.Infof("Wormhole cross-chain transfer broadcast: txHash=%s, bridgeID=%s, fee=%s", txHash, bridgeID, feeStr)

	w.mu.Lock()
	w.bridgeStatuses[bridgeID] = &model.BridgeStatus{
		BridgeID:   bridgeID,
		Status:     "PENDING",
		FromTxHash: txHash,
		ToTxHash:   "",
		FromChain:  req.FromChain,
		ToChain:    req.ToChain,
		Amount:     req.Amount,
		Protocol:   "wormhole",
		CreateTime: time.Now(),
	}
	w.mu.Unlock()

	return &model.BridgeResponse{
		TxHash:        txHash,
		BridgeID:      bridgeID,
		Protocol:      "wormhole",
		EstimatedTime: 360,
		Fee:           feeStr,
		CreateTime:    time.Now(),
	}, nil
}

// executeRealBridge 执行真实的 Wormhole 跨链转账
func (w *Wormhole) executeRealBridge(req *model.BridgeRequest, tokenAddress string) (string, *big.Int, error) {
	log := logger.GetLoggerInstance().Named("bridge.wormhole").Sugar()

	client, err := w.getEthClient(req.FromChain)
	if err != nil {
		return "", nil, fmt.Errorf("get ethclient: %w", err)
	}

	cfg := config.GetGlobalConfig()
	if cfg == nil || cfg.Wallet.PrivateSecret == "" {
		return "", nil, fmt.Errorf("private key not configured")
	}
	privateKey, err := crypto.HexToECDSA(strings.TrimPrefix(cfg.Wallet.PrivateSecret, "0x"))
	if err != nil {
		return "", nil, fmt.Errorf("invalid private key: %w", err)
	}
	fromAddress := crypto.PubkeyToAddress(privateKey.PublicKey)

	recipient := fromAddress
	if req.Recipient != "" {
		recipient = common.HexToAddress(req.Recipient)
	}
	recipientBytes32 := common.LeftPadBytes(recipient.Bytes(), 32)
	var recipientArr [32]byte
	copy(recipientArr[:], recipientBytes32)

	tokenContract := common.HexToAddress(tokenAddress)
	decimals, err := w.getTokenDecimals(context.Background(), client, tokenContract)
	if err != nil {
		log.Warnf("Failed to get decimals, assuming 18: %v", err)
		decimals = 18
	}

	var amountLD *big.Int
	amountLD, ok := new(big.Int).SetString(strings.TrimSpace(req.Amount), 10)
	if !ok {
		amountFloat, err := parseAmount(req.Amount)
		if err != nil {
			return "", nil, fmt.Errorf("invalid amount: %w", err)
		}
		amountLD = amountToAmountLD(amountFloat, decimals)
	}
	if amountLD.Sign() <= 0 {
		return "", nil, fmt.Errorf("amount must be positive")
	}

	targetWormholeChainID := GetWormholeChainID(req.ToChain)
	if targetWormholeChainID == 0 {
		return "", nil, fmt.Errorf("unsupported destination chain: %s", req.ToChain)
	}

	bridgeAddr := GetTokenBridgeAddress(req.FromChain)
	if bridgeAddr == "" {
		return "", nil, fmt.Errorf("Token Bridge not configured for chain %s", req.FromChain)
	}
	bridgeContract := common.HexToAddress(bridgeAddr)

	tbABI, err := w.getTokenBridgeABI()
	if err != nil {
		return "", nil, err
	}

	wormholeFee := big.NewInt(defaultWormholeFeeWei)
	nonce := uint32(time.Now().Unix() & 0xFFFFFFFF)
	arbiterFee := big.NewInt(0)

	calldata, err := tbABI.Pack("transferTokens",
		tokenContract,
		amountLD,
		targetWormholeChainID,
		recipientArr,
		arbiterFee,
		nonce,
	)
	if err != nil {
		return "", nil, fmt.Errorf("pack transferTokens: %w", err)
	}

	nonce64, err := client.PendingNonceAt(context.Background(), fromAddress)
	if err != nil {
		return "", nil, fmt.Errorf("get nonce: %w", err)
	}

	gasPrice, err := client.SuggestGasPrice(context.Background())
	if err != nil {
		return "", nil, fmt.Errorf("get gas price: %w", err)
	}

	gasLimit, err := client.EstimateGas(context.Background(), ethereum.CallMsg{
		From:  fromAddress,
		To:    &bridgeContract,
		Data:  calldata,
		Value: wormholeFee,
	})
	if err != nil {
		return "", nil, fmt.Errorf("estimate gas: %w", err)
	}
	gasLimit = gasLimit * 120 / 100

	chainIDInt, err := strconv.ParseInt(req.FromChain, 10, 64)
	if err != nil {
		return "", nil, fmt.Errorf("invalid chain ID: %w", err)
	}

	tx := types.NewTransaction(
		nonce64,
		bridgeContract,
		wormholeFee,
		gasLimit,
		gasPrice,
		calldata,
	)
	signer := types.LatestSignerForChainID(big.NewInt(chainIDInt))
	signedTx, err := types.SignTx(tx, signer, privateKey)
	if err != nil {
		return "", nil, fmt.Errorf("sign tx: %w", err)
	}

	if err := client.SendTransaction(context.Background(), signedTx); err != nil {
		return "", nil, fmt.Errorf("send tx: %w", err)
	}

	txHash := signedTx.Hash().Hex()
	totalFee := new(big.Int).Set(wormholeFee)
	totalFee.Add(totalFee, new(big.Int).Mul(gasPrice, big.NewInt(int64(gasLimit))))
	log.Infof("Wormhole bridge tx sent: txHash=%s, gasLimit=%d", txHash, gasLimit)

	return txHash, totalFee, nil
}

// GetBridgeStatus 查询跨链状态
func (w *Wormhole) GetBridgeStatus(bridgeID string, fromChain, toChain string) (*model.BridgeStatus, error) {
	log := logger.GetLoggerInstance().Named("bridge.wormhole").Sugar()

	if !w.enabled {
		return nil, fmt.Errorf("wormhole bridge is not enabled")
	}

	w.mu.RLock()
	memStatus, hasMem := w.bridgeStatuses[bridgeID]
	memCopy := memStatus
	w.mu.RUnlock()

	if hasMem && memCopy != nil {
		txHash := memCopy.FromTxHash
		if strings.HasPrefix(txHash, "0x") && len(txHash) == 66 {
			status, err := w.queryRealBridgeStatus(txHash, fromChain, toChain)
			if err == nil && status != nil {
				log.Debugf("Wormhole bridge status (from chain): bridgeID=%s, status=%s", bridgeID, status.Status)
				return status, nil
			}
			log.Debugf("Failed to query real bridge status, using memory: %v", err)
		}
		sc := *memCopy
		return &sc, nil
	}

	// bridgeID 可能是 txHash（Manager 传的是 txHash）
	if strings.HasPrefix(bridgeID, "0x") && len(bridgeID) == 66 {
		status, err := w.queryRealBridgeStatus(bridgeID, fromChain, toChain)
		if err == nil && status != nil {
			return status, nil
		}
	}

	log.Debugf("Wormhole bridge status not found: bridgeID=%s", bridgeID)
	return &model.BridgeStatus{
		BridgeID:   bridgeID,
		Status:     "PENDING",
		FromChain:  fromChain,
		ToChain:    toChain,
		Protocol:   "wormhole",
		CreateTime: time.Now(),
	}, nil
}

// whScanTransfer 解析 Wormholescan API 返回的 transfer 项
type whScanTransfer struct {
	Status    string `json:"status"`
	ToTxHash  string `json:"toTxHash"`
	ToChainID int    `json:"toChainId"`
}

// whScanResponse 解析 Wormholescan API 响应
type whScanResponse struct {
	Transfers []whScanTransfer `json:"transfers"`
}

// queryWormholescanByTx 通过源链 txHash 查询 Wormholescan API
func queryWormholescanByTx(ctx context.Context, sourceTxHash string) (status string, toTxHash string, err error) {
	url := wormholescanAPIBase + "/api/v1/vaa?txHash=" + sourceTxHash
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("wormholescan api status %d", resp.StatusCode)
	}
	var body struct {
		Data []struct {
			Transfers []struct {
				Status    string `json:"status"`
				ToTxHash  string `json:"toTxHash"`
				ToChainID int    `json:"toChainId"`
			} `json:"transfers"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", err
	}
	if len(body.Data) == 0 || len(body.Data[0].Transfers) == 0 {
		return "", "", nil
	}
	t := body.Data[0].Transfers[0]
	return t.Status, t.ToTxHash, nil
}

// queryRealBridgeStatus 查询链上跨链状态
func (w *Wormhole) queryRealBridgeStatus(txHash string, fromChain, toChain string) (*model.BridgeStatus, error) {
	log := logger.GetLoggerInstance().Named("bridge.wormhole").Sugar()

	var receipt *types.Receipt
	var err error
	if w.receiptFetcher != nil {
		receipt, err = w.receiptFetcher(context.Background(), fromChain, common.HexToHash(txHash))
	} else {
		client, clientErr := w.getEthClient(fromChain)
		if clientErr != nil {
			return nil, fmt.Errorf("get ethclient: %w", clientErr)
		}
		receipt, err = client.TransactionReceipt(context.Background(), common.HexToHash(txHash))
	}
	if err != nil {
		return &model.BridgeStatus{
			Status:     "PENDING",
			FromTxHash: txHash,
			FromChain:  fromChain,
			ToChain:    toChain,
			Protocol:   "wormhole",
		}, nil
	}

	if receipt.Status == 0 {
		return &model.BridgeStatus{
			Status:     "FAILED",
			FromTxHash: txHash,
			FromChain:  fromChain,
			ToChain:    toChain,
			Protocol:   "wormhole",
		}, nil
	}

	scanStatus, toTxHash, scanErr := queryWormholescanByTx(context.Background(), txHash)
	if scanErr != nil {
		log.Debugf("Wormholescan API query failed: %v", scanErr)
	}
	switch strings.ToUpper(scanStatus) {
	case "COMPLETED", "REDEEMED", "DELIVERED":
		now := time.Now()
		log.Infof("Wormhole transfer completed: fromTxHash=%s, toTxHash=%s", txHash, toTxHash)
		return &model.BridgeStatus{
			Status:       "COMPLETED",
			FromTxHash:   txHash,
			ToTxHash:     toTxHash,
			FromChain:    fromChain,
			ToChain:      toChain,
			Protocol:     "wormhole",
			CompleteTime: &now,
		}, nil
	case "FAILED":
		return &model.BridgeStatus{
			Status:     "FAILED",
			FromTxHash: txHash,
			FromChain:  fromChain,
			ToChain:    toChain,
			Protocol:   "wormhole",
		}, nil
	}

	return &model.BridgeStatus{
		Status:     "IN_PROGRESS",
		FromTxHash: txHash,
		FromChain:  fromChain,
		ToChain:    toChain,
		Protocol:   "wormhole",
	}, nil
}

// GetQuote 获取跨链报价
func (w *Wormhole) GetQuote(req *model.BridgeQuoteRequest) (*model.ProtocolQuote, error) {
	if !w.enabled {
		return nil, fmt.Errorf("wormhole bridge is not enabled")
	}
	if !w.IsChainPairSupported(req.FromChain, req.ToChain) {
		return &model.ProtocolQuote{
			Protocol:      "wormhole",
			Supported:     false,
			Fee:           "0",
			EstimatedTime: 0,
			MinAmount:     "0",
			MaxAmount:     "0",
		}, nil
	}

	fromToken := req.FromToken
	if fromToken == "" {
		fromToken = req.ToToken
	}
	toToken := req.ToToken
	if toToken == "" {
		toToken = req.FromToken
	}
	if fromToken == "" {
		fromToken = "USDT"
	}
	if toToken == "" {
		toToken = fromToken
	}

	fromAddr, fromOK := w.GetTokenContract(req.FromChain, fromToken)
	toAddr, toOK := w.GetTokenContract(req.ToChain, toToken)
	if !fromOK || !toOK {
		return &model.ProtocolQuote{
			Protocol:      "wormhole",
			Supported:     false,
			Fee:           "0",
			EstimatedTime: 0,
			MinAmount:     "0",
			MaxAmount:     "0",
			RawInfo: map[string]interface{}{
				"reason": "Wormhole token contract not found for this asset on fromChain or toChain",
			},
		}, nil
	}

	// 链对验证：需存在 origin→wrapped 或 wrapped→origin 的 Wormhole 桥接路径
	originWhID := GetWormholeChainID(req.FromChain)
	dstWhID := GetWormholeChainID(req.ToChain)
	if originWhID == 0 || dstWhID == 0 {
		return &model.ProtocolQuote{
			Protocol:      "wormhole",
			Supported:     false,
			Fee:           "0",
			EstimatedTime: 0,
			MinAmount:     "0",
			MaxAmount:     "0",
			RawInfo: map[string]interface{}{
				"reason": "Wormhole does not support this chain pair",
			},
		}, nil
	}
	// fromChain 为 origin → toChain 应有 wrapped 版本
	wrappedOnDst, err1 := w.queryWrappedAsset(req.ToChain, originWhID, fromAddr)
	// toChain 为 origin → fromChain 应有 wrapped 版本
	wrappedOnSrc, err2 := w.queryWrappedAsset(req.FromChain, dstWhID, toAddr)
	norm := func(s string) string { return strings.ToLower(strings.TrimPrefix(s, "0x")) }
	matchDst := err1 == nil && wrappedOnDst != "" && norm(wrappedOnDst) == norm(toAddr)
	matchSrc := err2 == nil && wrappedOnSrc != "" && norm(wrappedOnSrc) == norm(fromAddr)
	if !matchDst && !matchSrc {
		return &model.ProtocolQuote{
			Protocol:      "wormhole",
			Supported:     false,
			Fee:           "0",
			EstimatedTime: 0,
			MinAmount:     "0",
			MaxAmount:     "0",
			RawInfo: map[string]interface{}{
				"reason": "Wormhole wrappedAsset not found for this chain pair — token may not be bridgeable",
			},
		}, nil
	}

	return &model.ProtocolQuote{
		Protocol:      "wormhole",
		Supported:     true,
		Fee:           "0.5",
		EstimatedTime: 360,
		MinAmount:     "1",
		MaxAmount:     "0",
		RawInfo: map[string]interface{}{
			"feeType": "estimated_static",
			"note":    "Fee is an estimated average; actual fee depends on guardian network load",
		},
	}, nil
}

// IsChainSupported 检查是否支持该链
func (w *Wormhole) IsChainSupported(chainID string) bool {
	if !w.enabled {
		return false
	}
	supportedChains := map[string]bool{
		"1": true, "56": true, "137": true, "42161": true,
		"10": true, "43114": true, "8453": true, "250": true,
		"59144": true, "324": true, "5000": true, "534352": true,
	}
	return supportedChains[chainID]
}

// IsChainPairSupported 检查是否支持该链对
func (w *Wormhole) IsChainPairSupported(fromChain, toChain string) bool {
	return w.IsChainSupported(fromChain) && w.IsChainSupported(toChain) && fromChain != toChain
}

// DiscoverToken 自动发现 token 在各链上的可桥接地址。
// 策略：
//  1. 将 knownAddresses 中的已知 ERC-20 地址注册为 origin（源链 token）
//  2. 从每个有地址的 origin 链出发，通过 Token Bridge wrappedAsset() 查询
//     其他链上是否有 wrapped 版本，发现即注册
//  3. 最后将 origin 链本身也注册为 token 合约（用于从该链提现）
func (w *Wormhole) DiscoverToken(symbol string, knownAddresses map[string]string, targetChainIDs []string) (map[string]string, error) {
	if !w.enabled {
		return nil, nil
	}
	discovered := make(map[string]string)
	log := logger.GetLoggerInstance().Named("bridge.wormhole").Sugar()

	// wrappedAsset 发现：从每个有地址的链出发，查询其他链上是否有 wrapped 版本
	for _, originChainID := range targetChainIDs {
		originAddr, ok := knownAddresses[originChainID]
		if !ok || originAddr == "" || !w.IsChainSupported(originChainID) {
			continue
		}
		originWormholeID := GetWormholeChainID(originChainID)
		if originWormholeID == 0 {
			continue
		}

		// 注册 origin 链
		if _, ok := w.GetTokenContract(originChainID, symbol); !ok {
			w.SetTokenContract(originChainID, symbol, originAddr)
			discovered[originChainID] = originAddr
			log.Infof("DiscoverToken: registered %s on chain %s (origin), address=%s", symbol, originChainID, originAddr)
		}

		for _, targetChainID := range targetChainIDs {
			if targetChainID == originChainID || !w.IsChainSupported(targetChainID) {
				continue
			}
			// 跳过已通过 wrappedAsset 发现的链（避免重复查询），但不跳过 knownAddresses 注册的
			if addr, ok := discovered[targetChainID]; ok && addr != "" {
				continue
			}
			wrappedAddr, err := w.queryWrappedAsset(targetChainID, originWormholeID, originAddr)
			if err != nil {
				log.Debugf("DiscoverToken: wrappedAsset query %s chain %s->%s failed: %v", symbol, originChainID, targetChainID, err)
				continue
			}
			if wrappedAddr != "" {
				w.SetTokenContract(targetChainID, symbol, wrappedAddr)
				discovered[targetChainID] = wrappedAddr
				log.Infof("DiscoverToken: ✅ discovered %s on chain %s via wrappedAsset (origin chain %s), wrapped=%s", symbol, targetChainID, originChainID, wrappedAddr)
			}
		}
	}

	return discovered, nil
}

// queryWrappedAsset 调用目标链 Token Bridge 的 wrappedAsset(uint16, bytes32) 查询
// wrapped 版本的地址。返回空字符串表示该 token 未在目标链上 attest。
func (w *Wormhole) queryWrappedAsset(targetChainID string, originWormholeChainID uint16, originTokenAddr string) (string, error) {
	bridgeAddr := GetTokenBridgeAddress(targetChainID)
	if bridgeAddr == "" {
		return "", fmt.Errorf("no Token Bridge on chain %s", targetChainID)
	}

	client, err := w.getEthClient(targetChainID)
	if err != nil {
		return "", err
	}

	tbABI, err := w.getTokenBridgeABI()
	if err != nil {
		return "", err
	}

	var originBytes32 [32]byte
	addrBytes := common.HexToAddress(originTokenAddr).Bytes()
	copy(originBytes32[32-len(addrBytes):], addrBytes)

	calldata, err := tbABI.Pack("wrappedAsset", originWormholeChainID, originBytes32)
	if err != nil {
		return "", fmt.Errorf("pack wrappedAsset failed: %w", err)
	}

	bridgeContract := common.HexToAddress(bridgeAddr)
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &bridgeContract,
		Data: calldata,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("wrappedAsset call failed: %w", err)
	}

	if len(result) < 32 {
		return "", nil
	}

	addr := common.BytesToAddress(result[12:32])
	if addr == (common.Address{}) {
		return "", nil
	}
	return addr.Hex(), nil
}
