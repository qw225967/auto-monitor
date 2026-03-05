package ccip

import (
	"context"
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

	"crypto/ecdsa"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

var _ bridge.BridgeProtocol = (*CCIP)(nil)

// CCIP 实现 Chainlink CCIP 跨链协议
type CCIP struct {
	rpcURLs      map[string]string   // 主 RPC，chainID -> url
	rpcURLsList  map[string][]string  // 完整 RPC 列表（含备用），用于 verifyBroadcast 重广播
	enabled      bool

	routerAddresses map[string]string
	tokenPools      map[string]string

	bridgeStatuses map[string]*model.BridgeStatus
	mu             sync.RWMutex

	clients   map[string]*ethclient.Client
	clientsMu sync.Mutex

	routerABI *abi.ABI
	erc20ABI  *abi.ABI
	abiOnce   sync.Once
}

// NewCCIP 创建 CCIP 协议实例。rpcURLs 为主 RPC（chainID->url）；rpcURLsList 为完整列表（含备用），用于重广播。
func NewCCIP(rpcURLs map[string]string, enabled bool) *CCIP {
	if rpcURLs == nil {
		rpcURLs = make(map[string]string)
	}
	c := &CCIP{
		rpcURLs:         rpcURLs,
		rpcURLsList:     make(map[string][]string),
		enabled:         enabled,
		routerAddresses: make(map[string]string),
		tokenPools:      make(map[string]string),
		bridgeStatuses:  make(map[string]*model.BridgeStatus),
		clients:         make(map[string]*ethclient.Client),
	}
	c.initDefaultRouters()
	return c
}

// SetRPCURLsForChain 设置指定链的完整 RPC 列表（含备用），用于 verifyBroadcast 重广播。创建时由 web 层调用 GetDefaultRPCURLs 传入。
func (c *CCIP) SetRPCURLsForChain(chainID string, urls []string) {
	if c.rpcURLsList == nil {
		c.rpcURLsList = make(map[string][]string)
	}
	if len(urls) > 0 {
		c.rpcURLsList[chainID] = urls
	}
}

// initDefaultRouters 初始化主网 Router 合约地址
func (c *CCIP) initDefaultRouters() {
	defaults := map[string]string{
		"1":     "0x80226fc0Ee2b096224EeAc085Bb9a8cba1146f7D", // Ethereum
		"42161": "0x141fa059441E0ca23ce184B6A78bafD2A517DdE8", // Arbitrum
		"10":    "0x3206695CaE29952f4b0c22a169725a865bc8Ce0f", // Optimism
		"137":   "0x849c5ED5a80F5B408Dd4969b78c2C8fdf0565Bfe", // Polygon
		"43114": "0xF4c7E640EdA248ef95972845a62bdC74237805dB", // Avalanche
		"8453":  "0x881e3A65B4d4a04dD529061dd0071cf975F58bCD", // Base
		"56":    "0x34B03Cb9086d7D758AC55af71584F81A598759FE", // BSC
	}
	for chainID, addr := range defaults {
		if _, exists := c.routerAddresses[chainID]; !exists {
			c.routerAddresses[chainID] = addr
		}
	}
}

func (c *CCIP) SetRPCURL(chainID, rpcURL string) {
	if c.rpcURLs == nil {
		c.rpcURLs = make(map[string]string)
	}
	c.rpcURLs[chainID] = rpcURL
}

func (c *CCIP) SetRouterAddress(chainID, routerAddress string) {
	if c.routerAddresses == nil {
		c.routerAddresses = make(map[string]string)
	}
	c.routerAddresses[chainID] = routerAddress
}

func (c *CCIP) SetTokenPool(chainID, tokenSymbol, poolAddress string) {
	if c.tokenPools == nil {
		c.tokenPools = make(map[string]string)
	}
	key := fmt.Sprintf("%s:%s", chainID, tokenSymbol)
	c.tokenPools[key] = poolAddress
}

func (c *CCIP) GetName() string {
	return "ccip"
}

// ---------------------------------------------------------------------------
// Chain Selector 映射
// ---------------------------------------------------------------------------

// chainSelectors 将 EVM chainID 映射到 CCIP 的 uint64 chain selector
var chainSelectors = map[string]uint64{
	"1":     5009297550715157269,  // Ethereum
	"42161": 4949039107694359620,  // Arbitrum
	"10":    3734403246176062136,  // Optimism
	"137":   4051577828743386545,  // Polygon
	"43114": 6433500567565415381,  // Avalanche
	"8453":  15971525489660198786, // Base
	"56":    11344663589394136015, // BSC
}

func getChainSelector(chainID string) (uint64, bool) {
	sel, ok := chainSelectors[chainID]
	return sel, ok
}

// ---------------------------------------------------------------------------
// ABI 定义
// ---------------------------------------------------------------------------

const routerABIJSON = `[
  {
    "inputs": [
      {"internalType":"uint64","name":"destinationChainSelector","type":"uint64"},
      {
        "components": [
          {"internalType":"bytes","name":"receiver","type":"bytes"},
          {"internalType":"bytes","name":"data","type":"bytes"},
          {
            "components": [
              {"internalType":"address","name":"token","type":"address"},
              {"internalType":"uint256","name":"amount","type":"uint256"}
            ],
            "internalType":"struct Client.EVMTokenAmount[]","name":"tokenAmounts","type":"tuple[]"
          },
          {"internalType":"address","name":"feeToken","type":"address"},
          {"internalType":"bytes","name":"extraArgs","type":"bytes"}
        ],
        "internalType":"struct Client.EVM2AnyMessage","name":"message","type":"tuple"
      }
    ],
    "name":"ccipSend",
    "outputs":[{"internalType":"bytes32","name":"","type":"bytes32"}],
    "stateMutability":"payable",
    "type":"function"
  },
  {
    "inputs": [
      {"internalType":"uint64","name":"destinationChainSelector","type":"uint64"},
      {
        "components": [
          {"internalType":"bytes","name":"receiver","type":"bytes"},
          {"internalType":"bytes","name":"data","type":"bytes"},
          {
            "components": [
              {"internalType":"address","name":"token","type":"address"},
              {"internalType":"uint256","name":"amount","type":"uint256"}
            ],
            "internalType":"struct Client.EVMTokenAmount[]","name":"tokenAmounts","type":"tuple[]"
          },
          {"internalType":"address","name":"feeToken","type":"address"},
          {"internalType":"bytes","name":"extraArgs","type":"bytes"}
        ],
        "internalType":"struct Client.EVM2AnyMessage","name":"message","type":"tuple"
      }
    ],
    "name":"getFee",
    "outputs":[{"internalType":"uint256","name":"fee","type":"uint256"}],
    "stateMutability":"view",
    "type":"function"
  },
  {
    "inputs":[{"internalType":"uint64","name":"chainSelector","type":"uint64"}],
    "name":"isChainSupported",
    "outputs":[{"internalType":"bool","name":"supported","type":"bool"}],
    "stateMutability":"view",
    "type":"function"
  }
]`

const erc20ABIJSON = `[
  {
    "inputs":[
      {"internalType":"address","name":"spender","type":"address"},
      {"internalType":"uint256","name":"amount","type":"uint256"}
    ],
    "name":"approve",
    "outputs":[{"internalType":"bool","name":"","type":"bool"}],
    "stateMutability":"nonpayable",
    "type":"function"
  },
  {
    "inputs":[{"internalType":"address","name":"account","type":"address"}],
    "name":"balanceOf",
    "outputs":[{"internalType":"uint256","name":"","type":"uint256"}],
    "stateMutability":"view",
    "type":"function"
  },
  {
    "inputs":[
      {"internalType":"address","name":"owner","type":"address"},
      {"internalType":"address","name":"spender","type":"address"}
    ],
    "name":"allowance",
    "outputs":[{"internalType":"uint256","name":"","type":"uint256"}],
    "stateMutability":"view",
    "type":"function"
  },
  {
    "inputs":[],
    "name":"decimals",
    "outputs":[{"internalType":"uint8","name":"","type":"uint8"}],
    "stateMutability":"view",
    "type":"function"
  }
]`

func (c *CCIP) loadABIs() error {
	var loadErr error
	c.abiOnce.Do(func() {
		rABI, err := abi.JSON(strings.NewReader(routerABIJSON))
		if err != nil {
			loadErr = fmt.Errorf("failed to parse router ABI: %w", err)
			return
		}
		c.routerABI = &rABI

		eABI, err := abi.JSON(strings.NewReader(erc20ABIJSON))
		if err != nil {
			loadErr = fmt.Errorf("failed to parse ERC20 ABI: %w", err)
			return
		}
		c.erc20ABI = &eABI
	})
	return loadErr
}

// ---------------------------------------------------------------------------
// RPC 客户端管理
// ---------------------------------------------------------------------------

func (c *CCIP) getEthClient(chainID string) (*ethclient.Client, error) {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

	if client, ok := c.clients[chainID]; ok {
		return client, nil
	}

	// 优先级：CCIP.RPCURLs > c.rpcURLs(buildBridgeManager 传入) > LayerZero.RPCURLs > constants
	rpcURL := ""
	if cfg := config.GetGlobalConfig(); cfg != nil && cfg.Bridge.CCIP.RPCURLs != nil {
		if url, ok := cfg.Bridge.CCIP.RPCURLs[chainID]; ok && url != "" {
			rpcURL = url
		}
	}
	if rpcURL == "" {
		rpcURL = c.rpcURLs[chainID]
	}
	if rpcURL == "" {
		if cfg := config.GetGlobalConfig(); cfg != nil && cfg.Bridge.LayerZero.RPCURLs != nil {
			if url, ok := cfg.Bridge.LayerZero.RPCURLs[chainID]; ok && url != "" {
				rpcURL = url
			}
		}
	}
	if rpcURL == "" {
		rpcURL = constants.GetDefaultRPCURL(chainID)
	}
	if rpcURL == "" {
		return nil, fmt.Errorf("no RPC URL configured for chain %s", chainID)
	}

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RPC for chain %s: %w", chainID, err)
	}

	log := logger.GetLoggerInstance().Named("bridge.ccip").Sugar()
	log.Debugf("CCIP getEthClient: chainID=%s, rpc=%s (new connection)", chainID, maskRPCURL(rpcURL))

	c.clients[chainID] = client
	return client, nil
}

func (c *CCIP) clearClient(chainID string) {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()
	if client, ok := c.clients[chainID]; ok {
		client.Close()
		delete(c.clients, chainID)
	}
}

// ---------------------------------------------------------------------------
// CheckBridgeReady
// ---------------------------------------------------------------------------

func (c *CCIP) CheckBridgeReady(fromChain, toChain, tokenSymbol string) error {
	if !c.enabled {
		return fmt.Errorf("ccip bridge is not enabled")
	}
	if !c.IsChainPairSupported(fromChain, toChain) {
		return fmt.Errorf("ccip does not support chain pair %s -> %s", fromChain, toChain)
	}

	c.mu.RLock()
	_, hasRouter := c.routerAddresses[fromChain]
	fromKey := fmt.Sprintf("%s:%s", fromChain, tokenSymbol)
	toKey := fmt.Sprintf("%s:%s", toChain, tokenSymbol)
	_, hasFromPool := c.tokenPools[fromKey]
	_, hasToPool := c.tokenPools[toKey]
	c.mu.RUnlock()

	if !hasRouter {
		return fmt.Errorf("ccip router not configured for chain %s", fromChain)
	}
	if !hasFromPool {
		return fmt.Errorf("ccip token pool not configured for %s (use SetTokenPool to configure)", fromKey)
	}
	if !hasToPool {
		return fmt.Errorf("ccip token pool not configured for %s (use SetTokenPool to configure)", toKey)
	}
	return nil
}

// ---------------------------------------------------------------------------
// BridgeToken — 核心跨链逻辑
// ---------------------------------------------------------------------------

func (c *CCIP) BridgeToken(req *model.BridgeRequest) (*model.BridgeResponse, error) {
	log := logger.GetLoggerInstance().Named("bridge.ccip").Sugar()

	if !c.enabled {
		return nil, fmt.Errorf("ccip bridge is not enabled")
	}
	if !c.IsChainPairSupported(req.FromChain, req.ToChain) {
		return nil, fmt.Errorf("ccip does not support chain pair %s -> %s", req.FromChain, req.ToChain)
	}

	if err := c.loadABIs(); err != nil {
		return nil, err
	}

	c.mu.RLock()
	routerAddr, hasRouter := c.routerAddresses[req.FromChain]
	fromKey := fmt.Sprintf("%s:%s", req.FromChain, req.FromToken)
	tokenAddr, hasToken := c.tokenPools[fromKey]
	c.mu.RUnlock()

	if !hasRouter || routerAddr == "" {
		return nil, fmt.Errorf("CCIP Router not configured for chain %s", req.FromChain)
	}
	if !hasToken || tokenAddr == "" {
		return nil, fmt.Errorf("CCIP token address not configured for %s", fromKey)
	}

	dstSelector, ok := getChainSelector(req.ToChain)
	if !ok {
		return nil, fmt.Errorf("CCIP chain selector not found for chain %s", req.ToChain)
	}

	log.Infof("CCIP bridge start: %s -> %s, token=%s, amount=%s, recipient=%s, router=%s, tokenAddr=%s, dstSelector=%d",
		req.FromChain, req.ToChain, req.FromToken, req.Amount, req.Recipient, routerAddr, tokenAddr, dstSelector)

	txHash, fee, err := c.executeRealBridge(req, routerAddr, tokenAddr, dstSelector)
	if err != nil {
		return nil, fmt.Errorf("CCIP bridge execution failed: %w", err)
	}

	bridgeID := fmt.Sprintf("ccip-%s-%s-%s-%d", req.FromChain, req.ToChain, txHash, time.Now().UnixMilli())
	now := time.Now()

	c.mu.Lock()
	c.bridgeStatuses[bridgeID] = &model.BridgeStatus{
		BridgeID:   bridgeID,
		Status:     "PENDING",
		FromTxHash: txHash,
		FromChain:  req.FromChain,
		ToChain:    req.ToChain,
		Amount:     req.Amount,
		Protocol:   "ccip",
		CreateTime: now,
	}
	c.mu.Unlock()

	feeStr := "0"
	if fee != nil {
		feeStr = fee.String()
	}

	return &model.BridgeResponse{
		TxHash:        txHash,
		BridgeID:      bridgeID,
		Protocol:      "ccip",
		EstimatedTime: 300,
		Fee:           feeStr,
		CreateTime:    now,
	}, nil
}

// executeRealBridge 执行 CCIP 跨链转账
func (c *CCIP) executeRealBridge(req *model.BridgeRequest, routerAddr, tokenAddr string, dstSelector uint64) (string, *big.Int, error) {
	log := logger.GetLoggerInstance().Named("bridge.ccip").Sugar()

	client, err := c.getEthClient(req.FromChain)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get ethclient: %w", err)
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
	log.Debugf("CCIP executeRealBridge: fromAddress=%s...%s, chainID=%s", fromAddress.Hex()[:8], fromAddress.Hex()[len(fromAddress.Hex())-6:], req.FromChain)

	nonce, err := client.PendingNonceAt(context.Background(), fromAddress)
	if err != nil {
		if isRateLimited(err) {
			log.Warnf("RPC rate limited for chain %s, reconnecting…", req.FromChain)
			c.clearClient(req.FromChain)
			client, err = c.getEthClient(req.FromChain)
			if err != nil {
				return "", nil, fmt.Errorf("reconnect failed: %w", err)
			}
			nonce, err = client.PendingNonceAt(context.Background(), fromAddress)
		}
		if err != nil {
			return "", nil, fmt.Errorf("failed to get nonce: %w", err)
		}
	}
	log.Debugf("CCIP executeRealBridge: nonce=%d", nonce)

	gasPrice, err := client.SuggestGasPrice(context.Background())
	if err != nil {
		return "", nil, fmt.Errorf("failed to get gas price: %w", err)
	}
	// 加 15% 余量，避免 replacement transaction underpriced（与自身 pending tx 或网络拥堵竞争）
	gasPrice = new(big.Int).Mul(gasPrice, big.NewInt(115))
	gasPrice.Div(gasPrice, big.NewInt(100))
	log.Debugf("CCIP executeRealBridge: gasPrice=%s wei (base+15%%)", gasPrice.String())

	tokenContract := common.HexToAddress(tokenAddr)
	routerContract := common.HexToAddress(routerAddr)

	// 查询代币精度
	decimals, err := c.getTokenDecimals(client, tokenContract)
	if err != nil {
		log.Warnf("Failed to get token decimals, assuming 18: %v", err)
		decimals = 18
	}

	// 解析金额
	amountLD, ok := new(big.Int).SetString(strings.TrimSpace(req.Amount), 10)
	if !ok {
		amountFloat, parseErr := strconv.ParseFloat(strings.TrimSpace(req.Amount), 64)
		if parseErr != nil {
			return "", nil, fmt.Errorf("invalid amount: %s", req.Amount)
		}
		exp := new(big.Float).SetFloat64(1)
		for i := 0; i < int(decimals); i++ {
			exp.Mul(exp, new(big.Float).SetFloat64(10))
		}
		result := new(big.Float).Mul(new(big.Float).SetFloat64(amountFloat), exp)
		amountLD, _ = result.Int(nil)
	}
	if amountLD.Sign() <= 0 {
		return "", nil, fmt.Errorf("amount must be positive")
	}

	// 检查代币余额
	balance, err := c.getTokenBalance(client, tokenContract, fromAddress)
	if err != nil {
		log.Warnf("Failed to query token balance: %v", err)
	} else if balance.Cmp(amountLD) < 0 {
		log.Warnf("Insufficient token balance: have %s, need %s; capping to balance", balance.String(), amountLD.String())
		amountLD = balance
	}
	if amountLD.Sign() <= 0 {
		return "", nil, fmt.Errorf("insufficient token balance for CCIP: token=%s, balance=%s", tokenAddr, balance.String())
	}
	log.Infof("CCIP executeRealBridge: balance=%s, amountLD=%s, decimals=%d", balance.String(), amountLD.String(), decimals)

	// 构建 EVM2AnyMessage
	receiverAddr := fromAddress
	if req.Recipient != "" {
		receiverAddr = common.HexToAddress(req.Recipient)
	}

	type evmTokenAmount struct {
		Token  common.Address
		Amount *big.Int
	}
	type evm2AnyMessage struct {
		Receiver     []byte
		Data         []byte
		TokenAmounts []evmTokenAmount
		FeeToken     common.Address
		ExtraArgs    []byte
	}

	// CCIP receiver 需 ABI 编码：EVM 地址需 32 字节左填充（与 LayerZero/Wormhole 一致）
	receiverBytes := common.LeftPadBytes(receiverAddr.Bytes(), 32)
	msg := evm2AnyMessage{
		Receiver: receiverBytes,
		Data:     []byte{},
		TokenAmounts: []evmTokenAmount{
			{Token: tokenContract, Amount: amountLD},
		},
		FeeToken:  common.Address{}, // address(0) = 使用原生代币支付费用
		ExtraArgs: []byte{},
	}

	// 1) 查询 CCIP 费用
	feeCalldata, err := c.routerABI.Pack("getFee", dstSelector, msg)
	if err != nil {
		return "", nil, fmt.Errorf("failed to pack getFee: %w", err)
	}
	feeResult, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &routerContract,
		Data: feeCalldata,
	}, nil)

	nativeFee := new(big.Int)
	if err != nil {
		log.Warnf("getFee call failed, using fallback fee: %v", err)
		nativeFee.SetUint64(400000000000000) // 0.0004 native fallback
	} else {
		outputs, unpackErr := c.routerABI.Unpack("getFee", feeResult)
		if unpackErr != nil || len(outputs) == 0 {
			log.Warnf("Failed to unpack getFee result, using fallback fee")
			nativeFee.SetUint64(400000000000000)
		} else {
			nativeFee = outputs[0].(*big.Int)
		}
	}

	// 增加 10% 余量
	nativeFee = new(big.Int).Mul(nativeFee, big.NewInt(110))
	nativeFee.Div(nativeFee, big.NewInt(100))

	if nativeFee.Sign() <= 0 {
		nativeFee.SetUint64(400000000000000)
	}

	log.Infof("CCIP fee: %s wei (with 10%% buffer)", nativeFee.String())

	// 2) Approve Router 花费代币（若 allowance 不足会发送 approve tx 并等待确认）
	err = c.ensureApproval(client, privateKey, fromAddress, tokenContract, routerContract, amountLD, nonce, gasPrice, req.FromChain)
	if err != nil {
		return "", nil, fmt.Errorf("token approval failed: %w", err)
	}
	// 重新查询 nonce，避免 ensureApproval 跳过 approve 时 nonce 被错误递增，或链上有其他 pending tx
	nonce, err = client.PendingNonceAt(context.Background(), fromAddress)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get nonce after approval: %w", err)
	}
	log.Debugf("CCIP executeRealBridge: nonce after approval=%d", nonce)

	// 3) 调用 ccipSend
	sendCalldata, err := c.routerABI.Pack("ccipSend", dstSelector, msg)
	if err != nil {
		return "", nil, fmt.Errorf("failed to pack ccipSend: %w", err)
	}

	gasLimit, err := client.EstimateGas(context.Background(), ethereum.CallMsg{
		From:  fromAddress,
		To:    &routerContract,
		Data:  sendCalldata,
		Value: nativeFee,
	})
	if err != nil {
		return "", nil, fmt.Errorf("estimateGas for ccipSend failed: %w", err)
	}
	rawGas := gasLimit
	gasLimit = gasLimit * 130 / 100 // +30% 安全余量
	log.Debugf("CCIP executeRealBridge: estimateGas=%d, gasLimit(with 30%%)=%d", rawGas, gasLimit)

	chainID, _ := strconv.ParseInt(req.FromChain, 10, 64)

	tx := types.NewTransaction(
		nonce,
		routerContract,
		nativeFee,
		gasLimit,
		gasPrice,
		sendCalldata,
	)

	signer := types.LatestSignerForChainID(big.NewInt(chainID))
	signedTx, err := types.SignTx(tx, signer, privateKey)
	if err != nil {
		return "", nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	log.Infof("CCIP executeRealBridge: sending tx, nonce=%d, chainID=%d, value=%s wei", nonce, chainID, nativeFee.String())
	err = client.SendTransaction(context.Background(), signedTx)
	if err != nil {
		return "", nil, fmt.Errorf("failed to send transaction: %w", err)
	}

	txHash := signedTx.Hash().Hex()
	log.Infof("CCIP executeRealBridge: SendTransaction returned ok, txHash=%s, starting verifyBroadcast", txHash)
	rpcHint := c.rpcURLs[req.FromChain]
	if rpcHint == "" {
		if cfg := config.GetGlobalConfig(); cfg != nil && cfg.Bridge.CCIP.RPCURLs != nil {
			rpcHint, _ = cfg.Bridge.CCIP.RPCURLs[req.FromChain]
		}
	}
	if rpcHint == "" {
		rpcHint = constants.GetDefaultRPCURL(req.FromChain)
	}
	totalFee := new(big.Int).Add(nativeFee, new(big.Int).Mul(gasPrice, big.NewInt(int64(gasLimit))))
	log.Infof("CCIP bridge tx sent: txHash=%s, chainID=%s, rpc=%s, gasLimit=%d, totalFee=%s", txHash, req.FromChain, maskRPCURL(rpcHint), gasLimit, totalFee.String())

	// 广播验证：确保交易被节点接受并上链，避免 RPC 返回成功但未实际广播的情况
	if err := c.verifyBroadcast(client, signedTx, req.FromChain, txHash); err != nil {
		return "", nil, fmt.Errorf("broadcast verification failed: %w", err)
	}

	return txHash, totalFee, nil
}

// verifyBroadcast 轮询交易回执，确认交易已上链；超时则尝试备用 RPC 重新广播
func (c *CCIP) verifyBroadcast(client *ethclient.Client, signedTx *types.Transaction, chainID, txHash string) error {
	log := logger.GetLoggerInstance().Named("bridge.ccip").Sugar()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	log.Infof("CCIP verifyBroadcast: polling for receipt, txHash=%s, chainID=%s, maxWait=90s", txHash, chainID)
	pollCount := 0
	for {
		receipt, err := client.TransactionReceipt(ctx, signedTx.Hash())
		if err == nil && receipt != nil {
			if receipt.Status == 0 {
				log.Errorf("CCIP verifyBroadcast: tx reverted, txHash=%s, block=%d", txHash, receipt.BlockNumber.Uint64())
				return fmt.Errorf("transaction reverted on chain: %s", txHash)
			}
			log.Infof("CCIP verifyBroadcast: tx confirmed in block %d (after %d polls)", receipt.BlockNumber.Uint64(), pollCount)
			return nil
		}
		pollCount++
		if pollCount%10 == 1 {
			log.Debugf("CCIP verifyBroadcast: poll #%d, txHash=%s not yet confirmed (err=%v)", pollCount, txHash, err)
		}
		select {
		case <-ctx.Done():
			break
		case <-time.After(3 * time.Second):
			continue
		}
		break
	}

	log.Warnf("CCIP verifyBroadcast: primary RPC timeout after 90s (%d polls), txHash=%s, trying backup RPCs", pollCount, txHash)

	// 主 RPC 超时未确认，尝试备用 RPC 重新广播（usedURL 用于跳过已尝试的主 RPC）
	usedURL := ""
	if cfg := config.GetGlobalConfig(); cfg != nil && cfg.Bridge.CCIP.RPCURLs != nil {
		usedURL, _ = cfg.Bridge.CCIP.RPCURLs[chainID]
	}
	if usedURL == "" {
		usedURL = c.rpcURLs[chainID]
	}
	if usedURL == "" {
		if cfg := config.GetGlobalConfig(); cfg != nil && cfg.Bridge.LayerZero.RPCURLs != nil {
			usedURL, _ = cfg.Bridge.LayerZero.RPCURLs[chainID]
		}
	}
	if usedURL == "" {
		usedURL = constants.GetDefaultRPCURL(chainID)
	}

	// 优先使用创建时传入的完整列表（GetDefaultRPCURLs），否则回退到 constants
	backups := c.rpcURLsList[chainID]
	if len(backups) == 0 {
		backups = constants.GetDefaultRPCURLs(chainID)
	}
	log.Infof("CCIP verifyBroadcast: usedURL=%s, backupCount=%d", maskRPCURL(usedURL), len(backups))
	backupIdx := 0
	for _, url := range backups {
		if url == "" || url == usedURL {
			continue
		}
		backupIdx++
		log.Infof("CCIP verifyBroadcast: backup #%d, dialing %s", backupIdx, maskRPCURL(url))
		backupClient, err := ethclient.Dial(url)
		if err != nil {
			log.Warnf("CCIP verifyBroadcast: backup #%d dial failed for %s: %v", backupIdx, maskRPCURL(url), err)
			continue
		}
		if err := backupClient.SendTransaction(context.Background(), signedTx); err != nil {
			backupClient.Close()
			log.Warnf("CCIP verifyBroadcast: backup #%d SendTransaction failed for %s: %v", backupIdx, maskRPCURL(url), err)
			continue
		}
		log.Infof("CCIP verifyBroadcast: backup #%d re-broadcast ok via %s, polling 60s for receipt", backupIdx, maskRPCURL(url))

		verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 60*time.Second)
		timeout := false
		for !timeout {
			receipt, err := backupClient.TransactionReceipt(verifyCtx, signedTx.Hash())
			if err == nil && receipt != nil {
				verifyCancel()
				backupClient.Close()
				if receipt.Status == 0 {
					return fmt.Errorf("transaction reverted on chain: %s", txHash)
				}
				log.Infof("CCIP verifyBroadcast: tx confirmed in block %d (after backup #%d re-broadcast)", receipt.BlockNumber.Uint64(), backupIdx)
				return nil
			}
			select {
			case <-verifyCtx.Done():
				verifyCancel()
				backupClient.Close()
				log.Debugf("CCIP verifyBroadcast: backup #%d poll timeout, trying next", backupIdx)
				timeout = true
			case <-time.After(3 * time.Second):
			}
		}
	}

	log.Errorf("CCIP verifyBroadcast: all retries failed, txHash=%s, chainID=%s, tried %d backup RPCs", txHash, chainID, backupIdx)
	return fmt.Errorf("transaction not confirmed on chain after 90s and backup RPC retries. txHash=%s chainID=%s. Possible causes: RPC node did not broadcast, or use a more reliable RPC in config", txHash, chainID)
}

func maskRPCURL(url string) string {
	if len(url) < 30 {
		return "***"
	}
	return url[:15] + "..." + url[len(url)-8:]
}

// ensureApproval 检查并授权 Router 花费代币
func (c *CCIP) ensureApproval(
	client *ethclient.Client,
	privateKey *ecdsa.PrivateKey,
	owner, token, spender common.Address,
	amount *big.Int,
	nonce uint64,
	gasPrice *big.Int,
	chainIDStr string,
) error {
	// 查询当前 allowance
	allowanceData, err := c.erc20ABI.Pack("allowance", owner, spender)
	if err != nil {
		return fmt.Errorf("failed to pack allowance: %w", err)
	}
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &token,
		Data: allowanceData,
	}, nil)
	if err != nil {
		return fmt.Errorf("allowance query failed: %w", err)
	}

	outputs, err := c.erc20ABI.Unpack("allowance", result)
	if err != nil || len(outputs) == 0 {
		return fmt.Errorf("failed to unpack allowance")
	}
	currentAllowance := outputs[0].(*big.Int)

	if currentAllowance.Cmp(amount) >= 0 {
		return nil // 已有足够授权
	}

	log := logger.GetLoggerInstance().Named("bridge.ccip").Sugar()
	log.Infof("CCIP ensureApproval: allowance insufficient (current=%s, need=%s), sending approve tx", currentAllowance.String(), amount.String())

	// 授权 MaxUint256 避免反复 approve
	maxApproval := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	approveData, err := c.erc20ABI.Pack("approve", spender, maxApproval)
	if err != nil {
		return fmt.Errorf("failed to pack approve: %w", err)
	}

	gasLimit, err := client.EstimateGas(context.Background(), ethereum.CallMsg{
		From: owner,
		To:   &token,
		Data: approveData,
	})
	if err != nil {
		gasLimit = 60000
	}

	chainID, _ := strconv.ParseInt(chainIDStr, 10, 64)
	tx := types.NewTransaction(nonce, token, big.NewInt(0), gasLimit, gasPrice, approveData)
	signer := types.LatestSignerForChainID(big.NewInt(chainID))
	signedTx, err := types.SignTx(tx, signer, privateKey)
	if err != nil {
		return fmt.Errorf("failed to sign approve tx: %w", err)
	}

	err = client.SendTransaction(context.Background(), signedTx)
	if err != nil {
		return fmt.Errorf("failed to send approve tx: %w", err)
	}

	// 等待 approve 交易被确认
	log.Infof("CCIP ensureApproval: approve tx sent, txHash=%s, waiting for confirmation (max 120s)", signedTx.Hash().Hex())

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	for {
		receipt, err := client.TransactionReceipt(ctx, signedTx.Hash())
		if err == nil && receipt != nil {
			if receipt.Status == 0 {
				return fmt.Errorf("approve transaction reverted: %s", signedTx.Hash().Hex())
			}
			log.Infof("CCIP ensureApproval: approve confirmed in block %d", receipt.BlockNumber.Uint64())
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("approve tx confirmation timeout: %s", signedTx.Hash().Hex())
		case <-time.After(3 * time.Second):
		}
	}
}

// ---------------------------------------------------------------------------
// GetBridgeStatus
// ---------------------------------------------------------------------------

func (c *CCIP) GetBridgeStatus(bridgeID string, fromChain, toChain string) (*model.BridgeStatus, error) {
	log := logger.GetLoggerInstance().Named("bridge.ccip").Sugar()

	if !c.enabled {
		return nil, fmt.Errorf("ccip bridge is not enabled")
	}

	// 1) 从内存查找
	c.mu.RLock()
	memStatus, hasMemStatus := c.bridgeStatuses[bridgeID]
	c.mu.RUnlock()

	if !hasMemStatus || memStatus == nil {
		return nil, fmt.Errorf("ccip bridge status not found: %s", bridgeID)
	}

	if memStatus.Status == "COMPLETED" || memStatus.Status == "FAILED" {
		return memStatus, nil
	}

	txHash := memStatus.FromTxHash
	if txHash == "" {
		return memStatus, nil
	}

	// 2) 查询源链交易回执
	client, err := c.getEthClient(fromChain)
	if err != nil {
		log.Warnf("Failed to get client for status check: %v", err)
		return memStatus, nil
	}

	receipt, err := client.TransactionReceipt(context.Background(), common.HexToHash(txHash))
	if err != nil || receipt == nil {
		log.Debugf("CCIP GetBridgeStatus: source tx not yet confirmed, bridgeID=%s, txHash=%s, err=%v", bridgeID, txHash, err)
		return memStatus, nil
	}

	log.Debugf("CCIP GetBridgeStatus: source tx confirmed, bridgeID=%s, txHash=%s, block=%d, status=%d", bridgeID, txHash, receipt.BlockNumber.Uint64(), receipt.Status)

	if receipt.Status == 0 {
		c.mu.Lock()
		memStatus.Status = "FAILED"
		c.mu.Unlock()
		return memStatus, nil
	}

	// 源链交易已确认，标记为处理中
	c.mu.Lock()
	if memStatus.Status == "PENDING" {
		memStatus.Status = "IN_PROGRESS"
	}
	c.mu.Unlock()

	// 3) 通过 CCIP Explorer API 查询最终状态
	explorerStatus, toTxHash, queryErr := c.queryCCIPExplorerStatus(txHash)
	if queryErr != nil {
		log.Debugf("CCIP explorer query failed: %v", queryErr)
		// Explorer API 可能已废弃(404)，源链已确认时：超过 5 分钟则乐观标记 COMPLETED（币往往已到账，缩短等待）
		if strings.Contains(queryErr.Error(), "404") {
			elapsed := time.Since(memStatus.CreateTime)
			if elapsed >= 5*time.Minute {
				log.Infof("CCIP GetBridgeStatus: explorer API 404, source confirmed for %v, marking COMPLETED (dest tx unknown)", elapsed)
				c.mu.Lock()
				memStatus.Status = "COMPLETED"
				memStatus.ToTxHash = ""
				now := time.Now()
				memStatus.CompleteTime = &now
				c.mu.Unlock()
				return memStatus, nil
			}
		}
		return memStatus, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	switch explorerStatus {
	case "SUCCESS":
		memStatus.Status = "COMPLETED"
		memStatus.ToTxHash = toTxHash
		now := time.Now()
		memStatus.CompleteTime = &now
	case "FAILURE":
		memStatus.Status = "FAILED"
	default:
		memStatus.Status = "IN_PROGRESS"
	}

	return memStatus, nil
}

// queryCCIPExplorerStatus 通过 Chainlink CCIP Explorer 查询消息状态
func (c *CCIP) queryCCIPExplorerStatus(txHash string) (status string, toTxHash string, err error) {
	url := fmt.Sprintf("https://ccip.chain.link/api/h/atlas/message?sourceTransactionHash=%s", txHash)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("CCIP explorer returned status %d (API may be deprecated)", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			State                    string `json:"state"`
			DestTransactionHash      string `json:"destTransactionHash"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("failed to decode CCIP explorer response: %w", err)
	}

	if len(result.Data) == 0 {
		return "PENDING", "", nil
	}

	msg := result.Data[0]
	return msg.State, msg.DestTransactionHash, nil
}

// ---------------------------------------------------------------------------
// GetQuote
// ---------------------------------------------------------------------------

func (c *CCIP) GetQuote(req *model.BridgeQuoteRequest) (*model.ProtocolQuote, error) {
	if !c.enabled {
		return nil, fmt.Errorf("ccip bridge is not enabled")
	}

	if !c.IsChainPairSupported(req.FromChain, req.ToChain) {
		return &model.ProtocolQuote{
			Protocol:  "ccip",
			Supported: false,
		}, nil
	}

	fromToken := req.FromToken
	if fromToken == "" {
		fromToken = req.ToToken
	}
	if fromToken == "" {
		fromToken = "USDT"
	}

	c.mu.RLock()
	fromKey := fmt.Sprintf("%s:%s", req.FromChain, fromToken)
	toKey := fmt.Sprintf("%s:%s", req.ToChain, fromToken)
	tokenAddr, hasFromPool := c.tokenPools[fromKey]
	_, hasToPool := c.tokenPools[toKey]
	routerAddr, hasRouter := c.routerAddresses[req.FromChain]
	c.mu.RUnlock()

	if !hasFromPool || !hasToPool || !hasRouter {
		missing := make([]string, 0, 3)
		if !hasRouter {
			missing = append(missing, "router for chain "+req.FromChain)
		}
		if !hasFromPool {
			missing = append(missing, "token pool for "+fromKey)
		}
		if !hasToPool {
			missing = append(missing, "token pool for "+toKey)
		}
		return &model.ProtocolQuote{
			Protocol:  "ccip",
			Supported: false,
			RawInfo: map[string]interface{}{
				"reason": "CCIP not configured: missing " + fmt.Sprintf("%v", missing),
			},
		}, nil
	}

	dstSelector, ok := getChainSelector(req.ToChain)
	if !ok {
		return &model.ProtocolQuote{
			Protocol:  "ccip",
			Supported: false,
			RawInfo:   map[string]interface{}{"reason": "chain selector not found for " + req.ToChain},
		}, nil
	}

	// 尝试链上查询真实费用；如果 Router 拒绝（token 不被 CCIP 支持），返回 Supported: false
	if err := c.loadABIs(); err == nil {
		if realFee, feeErr := c.queryOnChainFee(req, routerAddr, tokenAddr, dstSelector); feeErr == nil {
			return &model.ProtocolQuote{
				Protocol:      "ccip",
				Supported:     true,
				Fee:           realFee,
				EstimatedTime: 300,
				MinAmount:     "0",
				MaxAmount:     "0",
			}, nil
		}
	}

	return &model.ProtocolQuote{
		Protocol:  "ccip",
		Supported: false,
		RawInfo: map[string]interface{}{
			"reason": "CCIP on-chain fee query failed — token may not be supported by CCIP on this chain pair",
		},
	}, nil
}

// queryOnChainFee 通过链上 Router.getFee() 查询跨链费用
func (c *CCIP) queryOnChainFee(req *model.BridgeQuoteRequest, routerAddr, tokenAddr string, dstSelector uint64) (string, error) {
	client, err := c.getEthClient(req.FromChain)
	if err != nil {
		return "", err
	}

	tokenContract := common.HexToAddress(tokenAddr)
	routerContract := common.HexToAddress(routerAddr)

	// 动态获取 token 精度，构造合理的 amount（1 个 token 单位）
	probeAmount := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil) // 默认 1e18
	if decimals, dErr := c.getTokenDecimals(client, tokenContract); dErr == nil && decimals > 0 {
		probeAmount = new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	}

	// 使用非零 dummy 地址作为 receiver（零地址可能被 Router 拒绝）
	dummyReceiver := common.HexToAddress("0x0000000000000000000000000000000000000001")

	// CCIP v1.5 extraArgs: version tag 0x97a657c9 + gasLimit=0 (让 Router 用默认值)
	extraArgs := common.Hex2Bytes("97a657c90000000000000000000000000000000000000000000000000000000000030d40")

	type evmTokenAmount struct {
		Token  common.Address
		Amount *big.Int
	}
	type evm2AnyMessage struct {
		Receiver     []byte
		Data         []byte
		TokenAmounts []evmTokenAmount
		FeeToken     common.Address
		ExtraArgs    []byte
	}

	msg := evm2AnyMessage{
		Receiver: dummyReceiver.Bytes(),
		Data:     []byte{},
		TokenAmounts: []evmTokenAmount{
			{Token: tokenContract, Amount: probeAmount},
		},
		FeeToken:  common.Address{},
		ExtraArgs: extraArgs,
	}

	calldata, err := c.routerABI.Pack("getFee", dstSelector, msg)
	if err != nil {
		return "", err
	}

	result, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &routerContract,
		Data: calldata,
	}, nil)
	if err != nil {
		return "", err
	}

	outputs, err := c.routerABI.Unpack("getFee", result)
	if err != nil || len(outputs) == 0 {
		return "", fmt.Errorf("failed to unpack getFee")
	}

	fee := outputs[0].(*big.Int)
	return fee.String(), nil
}

// ---------------------------------------------------------------------------
// 链支持检测
// ---------------------------------------------------------------------------

func (c *CCIP) IsChainSupported(chainID string) bool {
	if !c.enabled {
		return false
	}
	_, ok := chainSelectors[chainID]
	return ok
}

func (c *CCIP) IsChainPairSupported(fromChain, toChain string) bool {
	return c.IsChainSupported(fromChain) && c.IsChainSupported(toChain) && fromChain != toChain
}

// DiscoverToken 自动发现 token 在各链上是否被 CCIP 支持。
// 策略：注册已知 ERC-20 地址后，通过 Router.getFee() 链上验证 token 是否真正受支持。
func (c *CCIP) DiscoverToken(symbol string, knownAddresses map[string]string, targetChainIDs []string) (map[string]string, error) {
	if !c.enabled {
		return nil, nil
	}
	if err := c.loadABIs(); err != nil {
		return nil, fmt.Errorf("load ABI failed: %w", err)
	}
	log := logger.GetLoggerInstance().Named("bridge.ccip").Sugar()
	discovered := make(map[string]string)

	for chainID, addr := range knownAddresses {
		if addr == "" || !c.IsChainSupported(chainID) {
			continue
		}
		key := fmt.Sprintf("%s:%s", chainID, symbol)
		c.mu.RLock()
		_, exists := c.tokenPools[key]
		c.mu.RUnlock()
		if exists {
			continue
		}
		c.SetTokenPool(chainID, symbol, addr)
		discovered[chainID] = addr
	}

	// 用 getFee 验证每对链是否真正支持该 token
	verified := make(map[string]bool)
	for _, fromChainID := range targetChainIDs {
		fromAddr, ok := knownAddresses[fromChainID]
		if !ok || fromAddr == "" || fromAddr == "native" {
			continue
		}
		if !c.IsChainSupported(fromChainID) {
			continue
		}
		routerAddr, hasRouter := c.routerAddresses[fromChainID]
		if !hasRouter {
			log.Debugf("DiscoverToken: no CCIP router for chain %s, skip", fromChainID)
			continue
		}
		for _, toChainID := range targetChainIDs {
			if toChainID == fromChainID || !c.IsChainSupported(toChainID) {
				continue
			}
			dstSelector, ok := getChainSelector(toChainID)
			if !ok {
				continue
			}
			probeReq := &model.BridgeQuoteRequest{
				FromChain: fromChainID,
				ToChain:   toChainID,
				FromToken: symbol,
				Amount:    "1",
			}
			_, feeErr := c.queryOnChainFee(probeReq, routerAddr, fromAddr, dstSelector)
			if feeErr == nil {
				verified[fromChainID] = true
				verified[toChainID] = true
				log.Infof("DiscoverToken: ✅ CCIP supports %s on %s→%s", symbol, fromChainID, toChainID)
			} else {
				log.Debugf("DiscoverToken: CCIP getFee %s %s→%s failed: %v", symbol, fromChainID, toChainID, feeErr)
			}
		}
	}

	// 移除未通过验证的注册
	for chainID := range discovered {
		if !verified[chainID] {
			key := fmt.Sprintf("%s:%s", chainID, symbol)
			c.mu.Lock()
			delete(c.tokenPools, key)
			c.mu.Unlock()
			delete(discovered, chainID)
		}
	}

	return discovered, nil
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

func (c *CCIP) getTokenDecimals(client *ethclient.Client, token common.Address) (uint8, error) {
	data, err := c.erc20ABI.Pack("decimals")
	if err != nil {
		return 18, err
	}
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &token,
		Data: data,
	}, nil)
	if err != nil {
		return 18, err
	}
	outputs, err := c.erc20ABI.Unpack("decimals", result)
	if err != nil || len(outputs) == 0 {
		return 18, fmt.Errorf("failed to unpack decimals")
	}
	return outputs[0].(uint8), nil
}

func (c *CCIP) getTokenBalance(client *ethclient.Client, token, owner common.Address) (*big.Int, error) {
	data, err := c.erc20ABI.Pack("balanceOf", owner)
	if err != nil {
		return nil, err
	}
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &token,
		Data: data,
	}, nil)
	if err != nil {
		return nil, err
	}
	outputs, err := c.erc20ABI.Unpack("balanceOf", result)
	if err != nil || len(outputs) == 0 {
		return nil, fmt.Errorf("failed to unpack balanceOf")
	}
	return outputs[0].(*big.Int), nil
}

func isRateLimited(err error) bool {
	s := err.Error()
	return strings.Contains(s, "429") || strings.Contains(s, "Too Many Requests") || strings.Contains(s, "rate limit")
}
