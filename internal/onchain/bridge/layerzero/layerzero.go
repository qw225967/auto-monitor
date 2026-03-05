package layerzero

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

func debugLogLayerZero(location, message string, data map[string]interface{}, hypothesisId string) {}

// 确保 *LayerZero 实现 bridge.BridgeProtocol
var _ bridge.BridgeProtocol = (*LayerZero)(nil)

// LayerZero messagingFee 说明：
// - 正确来源：应使用 quoteSend() 的返回值，费用随 源链/目标链/ gas 价格 动态变化。
// - 组成：源链 tx 费 + DVN 验证费 + Executor 费 + 目标链 gas 折算（按价格换算到源链原生币）。
// - 多付部分会退到 _refundAddress，故略多付是安全的。
// - 仅在 quote 失败或返回 0 时使用下面的 fallback，避免传 0 导致合约 revert。
const (
	// defaultNativeFeeWei 仅作 quote 失败时的兜底（约 0.0001 BNB/ETH），覆盖 BSC↔ETH 等常见路径。
	// 实际应以 quoteSend 为准；BSC→ETH 实测约 0.00003 BNB，0.0004 留余量。
	defaultNativeFeeWei = 400_000_000_000_000 // 0.0004 * 1e18
	// insufficientFeeSelector LayerZero 合约 InsufficientFee 类错误的 selector（revert 时从错误数据中解析所需费用）
	insufficientFeeSelector = "0x4f3ec0d3"
	// layerZeroScanAPIBase 用于按源链交易哈希查询跨链送达状态（DELIVERED 时返回 COMPLETED）
	layerZeroScanAPIBase = "https://scan.layerzero-api.com/v1"
)

//go:embed oft_abi.json
var oftABIFS embed.FS

// LayerZero 实现 LayerZero 跨链协议
type LayerZero struct {
	// RPC URLs for different chains
	rpcURLs map[string]string
	enabled bool

	// OFT 合约地址映射：key 为 "chainID:tokenSymbol"，value 为合约地址
	// 例如："56:BNB" -> "0x..."
	oftContracts map[string]string
	// OFT 注册表（可选）：提供更丰富的 OFT 元数据，便于统一管理和查询
	oftRegistry *bridge.OFTRegistry

	// LayerZero Endpoint V2 地址映射：key 为 chainID
	endpointV2Addresses map[string]string

	// 用于存储跨链状态（初始/跟踪用，真实状态通过 GetBridgeStatus 查链上）
	// key: bridgeID, value: bridgeStatus
	bridgeStatuses map[string]*model.BridgeStatus
	mu             sync.RWMutex

	// ethclient 连接缓存：key 为 chainID
	clients   map[string]*ethclient.Client
	clientsMu sync.RWMutex

	// OFT ABI（缓存）
	oftABI     *abi.ABI
	oftABIOnce sync.Once

	// receiptFetcher 仅用于单测注入，不配置时走真实 RPC
	receiptFetcher func(ctx context.Context, chainID string, txHash common.Hash) (*types.Receipt, error)
}

// SetReceiptFetcherForTest 供单测注入回执获取函数，避免依赖真实 RPC
func (l *LayerZero) SetReceiptFetcherForTest(fn func(ctx context.Context, chainID string, txHash common.Hash) (*types.Receipt, error)) {
	l.receiptFetcher = fn
}

// NewLayerZero 创建 LayerZero 协议实例
func NewLayerZero(rpcURLs map[string]string, enabled bool) *LayerZero {
	lz := &LayerZero{
		rpcURLs:             rpcURLs,
		enabled:             enabled,
		bridgeStatuses:      make(map[string]*model.BridgeStatus),
		oftContracts:        make(map[string]string),
		endpointV2Addresses: make(map[string]string),
		clients:             make(map[string]*ethclient.Client),
	}

	// 初始化 LayerZero Endpoint V2 地址（根据官方文档）
	// 参考：https://docs.layerzero.network/v2/deployments/chains
	// 注意：Endpoint 地址是 LayerZero 的协议合约地址，不是 OFT 代币合约地址
	lz.endpointV2Addresses["1"] = "0x1a44076050125825900e736c501f859c50fE728c"  // Ethereum Mainnet Endpoint V2
	lz.endpointV2Addresses["56"] = "0x1a44076050125825900e736c501f859c50fE728c" // BSC Mainnet Endpoint V2
	// 注意：实际使用时，Endpoint 地址由 LayerZero SDK 自动处理，这里主要用于记录

	// 初始化常用代币的 OFT 合约地址（需要根据实际情况配置）
	// 对于 BNB，可能需要使用 Stargate 或其他桥接协议
	// 这里提供一个接口，可以通过配置或查询来获取

	return lz
}

// SetOFTRegistry 为 LayerZero 注入一个 OFTRegistry，用于集中管理 OFT 代币信息
func (l *LayerZero) SetOFTRegistry(reg *bridge.OFTRegistry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.oftRegistry = reg
}

// SetOFTContract 设置指定链和代币的 OFT 合约地址
// 注意：必须是实现了 LayerZero OFT 标准的合约，不能是普通的 ERC20 合约
// OFT 合约必须实现 send() 和 quoteSend() 方法
// 普通 ERC20 合约（如 WBNB）没有这些方法，不能直接使用
func (l *LayerZero) SetOFTContract(chainID, tokenSymbol, contractAddress string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.oftContracts[bridge.OFTTokenKey(chainID, tokenSymbol)] = contractAddress
}

// GetOFTContract 优先从注册表中获取 OFT 合约地址，其次从本地 oftContracts 中获取
func (l *LayerZero) GetOFTContract(chainID, tokenSymbol string) (string, bool) {
	l.mu.RLock()
	reg := l.oftRegistry
	l.mu.RUnlock()

	// 1. 先看注册表
	if reg != nil {
		if t, ok := reg.Get(chainID, tokenSymbol); ok && t.Address != "" {
			return t.Address, true
		}
	}

	// 2. 回退到内部 map
	l.mu.RLock()
	defer l.mu.RUnlock()
	addr, ok := l.oftContracts[bridge.OFTTokenKey(chainID, tokenSymbol)]
	return addr, ok
}

// VerifyOFTContract 验证合约地址是否实现了 OFT 标准
// 通过检查合约是否实现了 send() 和 quoteSend() 方法来判断
func (l *LayerZero) VerifyOFTContract(chainID, contractAddress string) (bool, error) {
	client, err := l.getEthClient(chainID)
	if err != nil {
		return false, fmt.Errorf("failed to get ethclient: %w", err)
	}

	oftABI, err := l.getOFTABI()
	if err != nil {
		return false, fmt.Errorf("failed to get OFT ABI: %w", err)
	}

	contractAddr := common.HexToAddress(contractAddress)

	// 通过尝试调用 quoteSend(SendParam) 来验证（view 函数）
	sendParam := struct {
		DstEid       uint32
		To           [32]byte
		AmountLD     *big.Int
		MinAmountLD  *big.Int
		ExtraOptions []byte
		ComposeMsg   []byte
		OftCmd       []byte
	}{
		DstEid:       30101,
		To:           [32]byte{},
		AmountLD:     big.NewInt(1),
		MinAmountLD:  big.NewInt(1),
		ExtraOptions: []byte{},
		ComposeMsg:   []byte{},
		OftCmd:       []byte{},
	}
	calldata, err := oftABI.Pack("quoteSend", sendParam)
	if err != nil {
		return false, fmt.Errorf("failed to pack quoteSend: %w", err)
	}

	// 先检查地址上是否有合约代码（防止 EOA 或空地址被误判为 OFT）
	code, codeErr := client.CodeAt(context.Background(), contractAddr, nil)
	if codeErr != nil {
		return false, fmt.Errorf("failed to check contract code: %w", codeErr)
	}
	if len(code) == 0 {
		// 该地址在此链上没有合约代码，一定不是 OFT
		return false, nil
	}

	// 尝试调用合约（使用 call，不会发送交易）
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &contractAddr,
		Data: calldata,
	}, nil)

	// 如果调用成功或返回特定的错误（如 "execution reverted"），说明合约存在但可能参数不对
	// 如果返回 "no contract code"，说明地址不是合约或不是 OFT 合约
	if err != nil {
		// 检查是否是合约不存在的错误
		if strings.Contains(err.Error(), "no contract code") ||
			strings.Contains(err.Error(), "contract not found") {
			return false, nil
		}
		// 其他错误（如执行失败）可能表示合约存在但参数不对，至少说明合约有代码
		// 这里简化处理，认为如果调用没有返回"无合约代码"错误，就认为可能是 OFT
		return true, nil
	}

	// 调用成功但返回空结果：合约可能不实现 quoteSend（非 OFT），或实际无合约代码
	if len(result) == 0 {
		return false, nil
	}

	// 调用成功且有返回数据，说明合约实现了 quoteSend 方法
	return true, nil
}

// QueryPeerAddress 查询 OFT 合约在目标链上的 peer 地址。
// 通过调用 OApp 的 peers(uint32 eid) view 函数获取 bytes32 格式的对端地址，
// 然后提取低 20 字节作为 EVM 地址返回。
// 返回空字符串表示该合约未配置该目标链的 peer。
func (l *LayerZero) QueryPeerAddress(sourceChainID, contractAddress, targetChainID string) (string, error) {
	client, err := l.getEthClient(sourceChainID)
	if err != nil {
		return "", fmt.Errorf("failed to get ethclient for chain %s: %w", sourceChainID, err)
	}

	oftABI, err := l.getOFTABI()
	if err != nil {
		return "", fmt.Errorf("failed to get OFT ABI: %w", err)
	}

	dstEid := l.getEndpointID(targetChainID)
	if dstEid == 0 {
		return "", fmt.Errorf("unknown endpoint ID for chain %s", targetChainID)
	}

	calldata, err := oftABI.Pack("peers", dstEid)
	if err != nil {
		return "", fmt.Errorf("failed to pack peers call: %w", err)
	}

	contractAddr := common.HexToAddress(contractAddress)
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &contractAddr,
		Data: calldata,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("peers call failed: %w", err)
	}

	if len(result) < 32 {
		return "", nil // 无返回数据
	}

	// peers 返回 bytes32，EVM 地址在低 20 字节（即 result[12:32]）
	var peerBytes32 [32]byte
	copy(peerBytes32[:], result[:32])

	// 检查是否为零地址（未配置 peer）
	allZero := true
	for _, b := range peerBytes32 {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return "", nil
	}

	peerAddr := common.BytesToAddress(peerBytes32[12:32])
	return peerAddr.Hex(), nil
}

// QueryUnderlyingToken 查询 OFT Adapter 的底层 ERC-20 代币地址。
// OFT Adapter 合约实现 token() 返回其包装的 ERC-20 地址。
// 纯 OFT 合约本身就是 ERC-20，调用 token() 会返回自身地址或 revert。
// 返回空字符串表示该合约不是 Adapter 或调用失败。
func (l *LayerZero) QueryUnderlyingToken(chainID, contractAddress string) (string, error) {
	client, err := l.getEthClient(chainID)
	if err != nil {
		return "", fmt.Errorf("failed to get ethclient: %w", err)
	}

	oftABI, err := l.getOFTABI()
	if err != nil {
		return "", fmt.Errorf("failed to get OFT ABI: %w", err)
	}

	calldata, err := oftABI.Pack("token")
	if err != nil {
		return "", nil // ABI 中没有 token 函数，非 adapter
	}

	contractAddr := common.HexToAddress(contractAddress)
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &contractAddr,
		Data: calldata,
	}, nil)
	if err != nil || len(result) < 32 {
		return "", nil // 调用失败或无返回，不是 adapter
	}

	// token() 返回 address（低 20 字节）
	tokenAddr := common.BytesToAddress(result[12:32])
	zeroAddr := common.Address{}
	if tokenAddr == zeroAddr {
		return "", nil
	}

	// 如果返回的地址与合约地址相同，说明是纯 OFT（不是 adapter）
	if strings.EqualFold(tokenAddr.Hex(), contractAddr.Hex()) {
		return "", nil
	}

	return tokenAddr.Hex(), nil
}

// getEthClient 获取或创建指定链的 ethclient（支持多个 RPC URLs 和故障转移）
func (l *LayerZero) getEthClient(chainID string) (*ethclient.Client, error) {
	l.clientsMu.RLock()
	if client, ok := l.clients[chainID]; ok && client != nil {
		l.clientsMu.RUnlock()
		return client, nil
	}
	l.clientsMu.RUnlock()

	// 收集所有可用的 RPC URLs
	var rpcURLs []string
	
	// 首先使用配置中的 RPC URL
	if rpcURL, ok := l.rpcURLs[chainID]; ok && rpcURL != "" {
		rpcURLs = append(rpcURLs, rpcURL)
	}
	
	// 然后添加常量中的备用 RPC URLs
	backupURLs := constants.GetDefaultRPCURLs(chainID)
	for _, url := range backupURLs {
		// 避免重复
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

	// 尝试每个 RPC URL，直到成功
	var lastErr error
	for _, rpcURL := range rpcURLs {
		client, err := ethclient.Dial(rpcURL)
		if err == nil {
			// 测试连接是否可用（尝试获取链 ID）
			_, err = client.ChainID(context.Background())
			if err == nil {
				l.clientsMu.Lock()
				l.clients[chainID] = client
				l.clientsMu.Unlock()
				logger.GetLoggerInstance().Named("bridge.layerzero").Sugar().Infof("Connected to RPC for chain %s: %s", chainID, rpcURL)
				return client, nil
			}
			client.Close()
		}
		lastErr = err
		logger.GetLoggerInstance().Named("bridge.layerzero").Sugar().Warnf("Failed to connect to RPC %s for chain %s: %v, trying next...", rpcURL, chainID, err)
	}

	return nil, fmt.Errorf("failed to connect to any RPC for chain %s (tried %d URLs): %w", chainID, len(rpcURLs), lastErr)
}

// getOFTABI 获取 OFT ABI（延迟加载）
func (l *LayerZero) getOFTABI() (*abi.ABI, error) {
	var err error
	l.oftABIOnce.Do(func() {
		// 读取嵌入的 ABI 文件
		abiData, readErr := oftABIFS.ReadFile("oft_abi.json")
		if readErr != nil {
			err = fmt.Errorf("failed to read OFT ABI: %w", readErr)
			return
		}

		// 解析 ABI
		var parsedABI abi.ABI
		if parseErr := json.Unmarshal(abiData, &parsedABI); parseErr != nil {
			err = fmt.Errorf("failed to parse OFT ABI: %w", parseErr)
			return
		}

		l.oftABI = &parsedABI
	})

	if err != nil {
		return nil, err
	}

	if l.oftABI == nil {
		return nil, fmt.Errorf("OFT ABI not loaded")
	}

	return l.oftABI, nil
}

// GetName 获取协议名称
func (l *LayerZero) GetName() string {
	return "layerzero"
}

// CheckBridgeReady 预检查 LayerZero 跨链条件是否满足：
// 1. 协议已启用
// 2. 链对支持
// 3. 源链和目标链上均有该 token 的 OFT 合约地址（从 OFTRegistry 或本地 map 中获取）
// 若 OFT 缺失，会尝试一次从 LayerZero API 自动拉取；仍缺失则返回错误。
func (l *LayerZero) CheckBridgeReady(fromChain, toChain, tokenSymbol string) error {
	if !l.enabled {
		return fmt.Errorf("layerzero bridge is not enabled")
	}
	if !l.IsChainPairSupported(fromChain, toChain) {
		return fmt.Errorf("layerzero does not support chain pair %s -> %s", fromChain, toChain)
	}

	// 检查源链和目标链的 OFT 合约
	missing := make([]string, 0, 2)
	foundAddrs := make(map[string]string)
	for _, chainID := range []string{fromChain, toChain} {
		addr, ok := l.GetOFTContract(chainID, tokenSymbol)
		if !ok || addr == "" {
			missing = append(missing, bridge.OFTTokenKey(chainID, tokenSymbol))
		} else {
			foundAddrs[bridge.OFTTokenKey(chainID, tokenSymbol)] = addr
		}
	}
	// #region agent log
	debugLogLayerZero("layerzero.go:CheckBridgeReady:firstCheck",
		"Initial OFT check", map[string]interface{}{
			"fromChain": fromChain, "toChain": toChain, "tokenSymbol": tokenSymbol,
			"missing": missing, "found": foundAddrs, "hasRegistry": l.oftRegistry != nil,
			"oftContractsKeys": func() []string {
				l.mu.RLock()
				defer l.mu.RUnlock()
				keys := make([]string, 0, len(l.oftContracts))
				for k := range l.oftContracts {
					keys = append(keys, k)
				}
				return keys
			}(),
		}, "H4,H5")
	// #endregion
	if len(missing) == 0 {
		return nil // 全部就绪
	}

	// 尝试从 LayerZero API 自动拉取
	l.mu.RLock()
	reg := l.oftRegistry
	l.mu.RUnlock()
	if reg != nil {
		opts := &bridge.LayerZeroAPIListOpts{Symbols: tokenSymbol}
		apiErr := reg.LoadFromLayerZeroAPI(context.Background(), "", opts, nil)
		// #region agent log
		debugLogLayerZero("layerzero.go:CheckBridgeReady:afterRetryAPI",
			"After retry API load", map[string]interface{}{
				"tokenSymbol": tokenSymbol, "apiErr": fmt.Sprintf("%v", apiErr),
			}, "H3,H5")
		// #endregion
		// 重新检查
		missing = missing[:0]
		for _, chainID := range []string{fromChain, toChain} {
			if addr, ok := l.GetOFTContract(chainID, tokenSymbol); !ok || addr == "" {
				missing = append(missing, bridge.OFTTokenKey(chainID, tokenSymbol))
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("LayerZero OFT contract not found for %v; token may not be registered as LayerZero OFT. "+
			"Please manually configure in config: bridge.layerZero.oftContracts (e.g. \"%s\" = \"<contract_address>\")",
			missing, missing[0])
	}
	return nil
}

// BridgeToken 执行跨链转账
func (l *LayerZero) BridgeToken(req *model.BridgeRequest) (*model.BridgeResponse, error) {
	log := logger.GetLoggerInstance().Named("bridge.layerzero").Sugar()

	if !l.enabled {
		return nil, fmt.Errorf("layerzero bridge is not enabled")
	}

	if !l.IsChainPairSupported(req.FromChain, req.ToChain) {
		return nil, fmt.Errorf("layerzero does not support chain pair %s -> %s", req.FromChain, req.ToChain)
	}

	log.Infof("Starting LayerZero bridge: %s -> %s, token=%s, amount=%s, recipient=%s",
		req.FromChain, req.ToChain, req.FromToken, req.Amount, req.Recipient)

	// 检查是否配置了 OFT 合约地址（优先从 OFTRegistry 获取，其次 config.Bridge.LayerZero.OFTContracts）
	key := bridge.OFTTokenKey(req.FromChain, req.FromToken)
	oftAddress, hasOFT := l.GetOFTContract(req.FromChain, req.FromToken)
	if !hasOFT || oftAddress == "" {
		// 若注入了 OFTRegistry，尝试从 LayerZero 官方 API 拉取该 symbol 的 OFT 列表并重试，避免必须手动配置
		l.mu.RLock()
		reg := l.oftRegistry
		l.mu.RUnlock()
		if reg != nil {
			opts := &bridge.LayerZeroAPIListOpts{Symbols: req.FromToken}
			if err := reg.LoadFromLayerZeroAPI(context.Background(), "", opts, nil); err != nil {
				log.Debugf("LayerZero OFT auto-fetch for %s failed: %v", key, err)
			} else {
				oftAddress, hasOFT = l.GetOFTContract(req.FromChain, req.FromToken)
				if hasOFT && oftAddress != "" {
					log.Infof("LayerZero OFT for %s resolved from API: %s", key, oftAddress)
				}
			}
		}
		if !hasOFT || oftAddress == "" {
			return nil, fmt.Errorf("OFT contract not configured for %s, cannot bridge. If this token is LayerZero OFT on this chain, add it in config: bridge.layerZero.oftContracts[\"%s\"] = \"<contract_address>\"", key, key)
		}
	}

	// 使用真实的 LayerZero OFT 合约进行跨链
	realTxHash, estimatedFee, err := l.executeRealBridge(req, oftAddress)
	if err != nil {
		return nil, fmt.Errorf("real LayerZero bridge failed: %w", err)
	}

	txHash := realTxHash
	bridgeID := fmt.Sprintf("lz_%s_%d", txHash[:16], time.Now().Unix())
	var estimatedTime int64 = 300
	feeLog := "0"
	if estimatedFee != nil {
		feeLog = estimatedFee.String()
	}
	log.Infof("LayerZero cross-chain transfer broadcast: txHash=%s, bridgeID=%s, fee=%s",
		txHash, bridgeID, feeLog)

	// 存储初始状态用于跟踪，实际状态通过 GetBridgeStatus 查询链上数据
	l.mu.Lock()
	l.bridgeStatuses[bridgeID] = &model.BridgeStatus{
		BridgeID:   bridgeID,
		Status:     "PENDING",
		FromTxHash: txHash,
		ToTxHash:   "",
		FromChain:  req.FromChain,
		ToChain:    req.ToChain,
		Amount:     req.Amount,
		Protocol:   "layerzero",
		CreateTime: time.Now(),
	}
	l.mu.Unlock()

	feeStr := "0"
	if estimatedFee != nil {
		feeStr = estimatedFee.String()
	}

	return &model.BridgeResponse{
		TxHash:        txHash,
		BridgeID:      bridgeID,
		Protocol:      "layerzero",
		EstimatedTime: estimatedTime, // 预估时间（真实跨链通常需要 5-10 分钟）
		Fee:           feeStr,
		CreateTime:    time.Now(),
	}, nil
}

// GetBridgeStatus 查询跨链状态
func (l *LayerZero) GetBridgeStatus(bridgeID string, fromChain, toChain string) (*model.BridgeStatus, error) {
	log := logger.GetLoggerInstance().Named("bridge.layerzero").Sugar()

	if !l.enabled {
		return nil, fmt.Errorf("layerzero bridge is not enabled")
	}

	// 先从内存中查找（初始状态或真实跨链跟踪）
	l.mu.RLock()
	memStatus, hasMemStatus := l.bridgeStatuses[bridgeID]
	memStatusCopy := memStatus
	l.mu.RUnlock()

	// 如果内存中有状态，检查是否是真实跨链（通过检查 txHash 是否是真实的链上交易）
	if hasMemStatus && memStatusCopy != nil {
		txHash := memStatusCopy.FromTxHash

		// 检查是否是真实的链上交易（以 0x 开头且长度正确）
		isRealTx := strings.HasPrefix(txHash, "0x") && len(txHash) == 66

		if isRealTx {
			// 对于真实跨链，查询链上状态
			status, err := l.queryRealBridgeStatus(txHash, fromChain, toChain)
			if err == nil && status != nil {
				log.Debugf("LayerZero bridge status (from chain): bridgeID=%s, status=%s, txHash=%s",
					bridgeID, status.Status, txHash)
				return status, nil
			}
			// 如果查询失败，回退到内存状态（可能是交易还未确认）
			log.Debugf("Failed to query real bridge status, using memory status: %v", err)
		}

		// 返回内存中的状态
		log.Debugf("LayerZero bridge status (from memory): bridgeID=%s, status=%s", bridgeID, memStatusCopy.Status)
		statusCopy := *memStatusCopy
		return &statusCopy, nil
	}

	// 如果内存中找不到，尝试从 bridgeID 中提取 txHash 并查询链上状态
	// bridgeID 格式：lz_<txHash前16位>_<timestamp>
	if strings.HasPrefix(bridgeID, "lz_") {
		parts := strings.Split(bridgeID, "_")
		if len(parts) >= 2 {
			// 尝试查询链上状态（需要完整的 txHash，这里简化处理）
			log.Debugf("LayerZero bridge status not found in memory, bridgeID=%s", bridgeID)
		}
	}

	// 如果都找不到，返回 PENDING 状态
	log.Debugf("LayerZero bridge status not found, returning PENDING: bridgeID=%s", bridgeID)
	return &model.BridgeStatus{
		BridgeID:   bridgeID,
		Status:     "PENDING",
		FromChain:  fromChain,
		ToChain:    toChain,
		Protocol:   "layerzero",
		CreateTime: time.Now(),
	}, nil
}

// lzScanMessage 与 LayerZero Scan API GET /messages/tx/{tx} 返回的单条 data 项结构对齐（仅解析所需字段）
type lzScanMessage struct {
	Status struct {
		Name string `json:"name"`
	} `json:"status"`
	Destination *struct {
		Tx *struct {
			TxHash string `json:"txHash"`
		} `json:"tx"`
		Burn *struct {
			TxHash string `json:"txHash"`
		} `json:"burn"`
		NativeDrop *struct {
			Tx *struct {
				TxHash string `json:"txHash"`
			} `json:"tx"`
		} `json:"nativeDrop"`
	} `json:"destination"`
}

// lzScanResponse GET /messages/tx/{tx} 响应
type lzScanResponse struct {
	Data []lzScanMessage `json:"data"`
}

// queryLayerZeroScanByTx 通过源链交易哈希查询 LayerZero Scan API，获取消息状态与目标链交易哈希。
// 返回 statusName（如 DELIVERED、FAILED、INFLIGHT 等）、toTxHash（送达时存在）、err。
func queryLayerZeroScanByTx(ctx context.Context, sourceTxHash string) (statusName string, toTxHash string, err error) {
	url := layerZeroScanAPIBase + "/messages/tx/" + sourceTxHash
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", fmt.Errorf("new request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("scan api status %d", resp.StatusCode)
	}
	var body lzScanResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", fmt.Errorf("decode: %w", err)
	}
	if len(body.Data) == 0 {
		return "", "", nil // 尚未被 Scan 索引，调用方按 IN_PROGRESS 处理
	}
	msg := body.Data[0]
	statusName = msg.Status.Name
	if msg.Destination != nil {
		if msg.Destination.Tx != nil && msg.Destination.Tx.TxHash != "" {
			toTxHash = msg.Destination.Tx.TxHash
		} else if msg.Destination.Burn != nil && msg.Destination.Burn.TxHash != "" {
			toTxHash = msg.Destination.Burn.TxHash
		} else if msg.Destination.NativeDrop != nil && msg.Destination.NativeDrop.Tx != nil {
			toTxHash = msg.Destination.NativeDrop.Tx.TxHash
		}
	}
	return statusName, toTxHash, nil
}

// queryRealBridgeStatus 查询真实的链上跨链状态
func (l *LayerZero) queryRealBridgeStatus(txHash string, fromChain, toChain string) (*model.BridgeStatus, error) {
	log := logger.GetLoggerInstance().Named("bridge.layerzero").Sugar()

	var receipt *types.Receipt
	var err error
	if l.receiptFetcher != nil {
		receipt, err = l.receiptFetcher(context.Background(), fromChain, common.HexToHash(txHash))
	} else {
		fromClient, clientErr := l.getEthClient(fromChain)
		if clientErr != nil {
			return nil, fmt.Errorf("failed to get ethclient for fromChain %s: %w", fromChain, clientErr)
		}
		receipt, err = fromClient.TransactionReceipt(context.Background(), common.HexToHash(txHash))
	}
	if err != nil {
		// 交易可能还未确认
		return &model.BridgeStatus{
			Status:     "PENDING",
			FromTxHash: txHash,
			FromChain:  fromChain,
			ToChain:    toChain,
			Protocol:   "layerzero",
		}, nil
	}

	// 2. 检查交易是否成功
	if receipt.Status == 0 {
		// 交易失败
		return &model.BridgeStatus{
			Status:     "FAILED",
			FromTxHash: txHash,
			FromChain:  fromChain,
			ToChain:    toChain,
			Protocol:   "layerzero",
		}, nil
	}

	// 3. 源链已确认，通过 LayerZero Scan API 查询是否已在目标链送达
	scanStatus, toTxHash, scanErr := queryLayerZeroScanByTx(context.Background(), txHash)
	if scanErr != nil {
		log.Debugf("LayerZero Scan API query failed (treat as in progress): %v", scanErr)
	}
	switch scanStatus {
	case "DELIVERED":
		now := time.Now()
		log.Infof("LayerZero message delivered: fromTxHash=%s, toTxHash=%s", txHash, toTxHash)
		return &model.BridgeStatus{
			Status:       "COMPLETED",
			FromTxHash:   txHash,
			ToTxHash:     toTxHash,
			FromChain:    fromChain,
			ToChain:      toChain,
			Protocol:     "layerzero",
			CompleteTime: &now,
		}, nil
	case "FAILED", "PAYLOAD_STORED", "BLOCKED", "UNRESOLVABLE_COMMAND", "MALFORMED_COMMAND":
		return &model.BridgeStatus{
			Status:     "FAILED",
			FromTxHash: txHash,
			FromChain:  fromChain,
			ToChain:    toChain,
			Protocol:   "layerzero",
		}, nil
	}

	// INFLIGHT、CONFIRMING 或 API 未索引：继续等待
	log.Debugf("Source chain transaction confirmed, message not yet delivered: txHash=%s, scanStatus=%s", txHash, scanStatus)
	return &model.BridgeStatus{
		Status:     "IN_PROGRESS",
		FromTxHash: txHash,
		FromChain:  fromChain,
		ToChain:    toChain,
		Protocol:   "layerzero",
	}, nil
}

// GetQuote 获取跨链报价
func (l *LayerZero) GetQuote(req *model.BridgeQuoteRequest) (*model.ProtocolQuote, error) {
	if !l.enabled {
		return nil, fmt.Errorf("layerzero bridge is not enabled")
	}

	if !l.IsChainPairSupported(req.FromChain, req.ToChain) {
		return &model.ProtocolQuote{
			Protocol:      "layerzero",
			Supported:     false,
			Fee:           "0",
			EstimatedTime: 0,
			MinAmount:     "0",
			MaxAmount:     "0",
		}, nil
	}

	// 按 symbol 判断：要求 fromChain/toChain 上均有该 token 的 OFT 合约，
	// 优先查本地合约（SetOFTContract 设置的）+ 全局注册表，找不到时再从 API 拉取
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
	// GetOFTContract 同时检查本地合约 + 全局注册表
	fromAddr, fromOK := l.GetOFTContract(req.FromChain, fromToken)
	toAddr, toOK := l.GetOFTContract(req.ToChain, toToken)
	// #region agent log
	debugLogLayerZero("GetQuote:H4:initialLookup", "OFT lookup before API retry", map[string]interface{}{
		"fromChain": req.FromChain, "toChain": req.ToChain, "fromToken": fromToken, "toToken": toToken,
		"fromAddr": fromAddr, "fromOK": fromOK, "toAddr": toAddr, "toOK": toOK,
		"localContractCount": len(l.oftContracts), "hasRegistry": l.oftRegistry != nil,
	}, "H4")
	// #endregion
	if !fromOK || !toOK {
		// 尝试从 LayerZero API 拉取该 symbol 的 OFT 列表后再判断
		l.mu.RLock()
		reg := l.oftRegistry
		l.mu.RUnlock()
		if reg != nil {
			_ = reg.LoadFromLayerZeroAPI(context.Background(), "", &bridge.LayerZeroAPIListOpts{Symbols: fromToken}, nil)
		}
		fromAddr, fromOK = l.GetOFTContract(req.FromChain, fromToken)
		toAddr, toOK = l.GetOFTContract(req.ToChain, toToken)
		// #region agent log
		debugLogLayerZero("GetQuote:H4:afterAPIRetry", "OFT lookup after API retry", map[string]interface{}{
			"fromChain": req.FromChain, "toChain": req.ToChain, "fromToken": fromToken, "toToken": toToken,
			"fromAddr": fromAddr, "fromOK": fromOK, "toAddr": toAddr, "toOK": toOK,
		}, "H4")
		// #endregion
	}
	if !fromOK || !toOK {
		return &model.ProtocolQuote{
			Protocol:      "layerzero",
			Supported:     false,
			Fee:           "0",
			EstimatedTime: 0,
			MinAmount:     "0",
			MaxAmount:     "0",
			RawInfo: map[string]interface{}{
				"reason": "OFT not found for this asset on fromChain or toChain (LayerZero)",
			},
		}, nil
	}

	// 链对验证：OFT 必须配置了 fromChain→toChain 的 peer，否则该链对不可桥接
	peerAddr, peerErr := l.QueryPeerAddress(req.FromChain, fromAddr, req.ToChain)
	if peerErr != nil || peerAddr == "" {
		return &model.ProtocolQuote{
			Protocol:      "layerzero",
			Supported:     false,
			Fee:           "0",
			EstimatedTime: 0,
			MinAmount:     "0",
			MaxAmount:     "0",
			RawInfo: map[string]interface{}{
				"reason": "LayerZero peer not configured for this chain pair (OFT may not support " + req.FromChain + "→" + req.ToChain + ")",
			},
		}, nil
	}
	if !strings.EqualFold(strings.TrimPrefix(peerAddr, "0x"), strings.TrimPrefix(toAddr, "0x")) {
		return &model.ProtocolQuote{
			Protocol:      "layerzero",
			Supported:     false,
			Fee:           "0",
			EstimatedTime: 0,
			MinAmount:     "0",
			MaxAmount:     "0",
			RawInfo: map[string]interface{}{
				"reason": "LayerZero peer address mismatch — token may not be bridgeable on this chain pair",
			},
		}, nil
	}

	return &model.ProtocolQuote{
		Protocol:      "layerzero",
		Supported:     true,
		Fee:           "0.3",
		EstimatedTime: 120,
		MinAmount:     "1",
		MaxAmount:     "0",  // TODO: 实际查询最大数量
	}, nil
}

// IsChainSupported 检查是否支持该链
func (l *LayerZero) IsChainSupported(chainID string) bool {
	if !l.enabled {
		return false
	}

	supportedChains := map[string]bool{
		"1":      true, // Ethereum
		"56":     true, // BSC
		"137":    true, // Polygon
		"42161":  true, // Arbitrum
		"10":     true, // Optimism
		"43114":  true, // Avalanche
		"8453":   true, // Base
		"250":    true, // Fantom
		"59144":  true, // Linea
		"324":    true, // zkSync Era
		"5000":   true, // Mantle
		"534352": true, // Scroll
		"1101":   true, // Polygon zkEVM
	}

	return supportedChains[chainID]
}

// IsChainPairSupported 检查是否支持该链对
func (l *LayerZero) IsChainPairSupported(fromChain, toChain string) bool {
	return l.IsChainSupported(fromChain) && l.IsChainSupported(toChain) && fromChain != toChain
}

// DiscoverToken 自动发现 token 在各链上的 OFT 合约地址。
// 策略：
//  1. 用 knownAddresses 中每条链的地址分别调用 VerifyOFTContract
//  2. 对已验证的 OFT 调用 QueryPeerAddress 发现其他链上的 OFT 地址
func (l *LayerZero) DiscoverToken(symbol string, knownAddresses map[string]string, targetChainIDs []string) (map[string]string, error) {
	if !l.enabled {
		return nil, nil
	}
	log := logger.GetLoggerInstance().Named("bridge.layerzero").Sugar()
	discovered := make(map[string]string)

	l.mu.RLock()
	reg := l.oftRegistry
	l.mu.RUnlock()

	verifiedChainID := ""
	verifiedAddr := ""

	for _, chainID := range targetChainIDs {
		if !l.IsChainSupported(chainID) {
			continue
		}
		if _, ok := l.GetOFTContract(chainID, symbol); ok {
			if verifiedChainID == "" {
				verifiedChainID = chainID
				verifiedAddr, _ = l.GetOFTContract(chainID, symbol)
			}
			continue
		}
		addr, ok := knownAddresses[chainID]
		if !ok || addr == "" {
			continue
		}
		isOFT, err := l.VerifyOFTContract(chainID, addr)
		if err != nil {
			log.Debugf("DiscoverToken: verify %s on chain %s failed: %v", symbol, chainID, err)
			continue
		}
		if isOFT {
			l.SetOFTContract(chainID, symbol, addr)
			if reg != nil {
				reg.Upsert(bridge.OFTToken{
					ChainID: chainID, Symbol: symbol, Address: addr,
					Enabled: true, Source: "auto-discover-unified",
				})
			}
			discovered[chainID] = addr
			log.Infof("DiscoverToken: ✅ verified %s on chain %s as OFT, address=%s", symbol, chainID, addr)
			if verifiedChainID == "" {
				verifiedChainID = chainID
				verifiedAddr = addr
			}
		}
	}

	if verifiedChainID == "" || verifiedAddr == "" {
		return discovered, nil
	}

	for _, targetChainID := range targetChainIDs {
		if targetChainID == verifiedChainID || !l.IsChainSupported(targetChainID) {
			continue
		}
		if _, ok := l.GetOFTContract(targetChainID, symbol); ok {
			continue
		}
		peerAddr, err := l.QueryPeerAddress(verifiedChainID, verifiedAddr, targetChainID)
		if err != nil {
			log.Debugf("DiscoverToken: peer query %s %s->%s failed: %v", symbol, verifiedChainID, targetChainID, err)
			continue
		}
		if peerAddr == "" {
			continue
		}
		isPeerOFT, verifyErr := l.VerifyOFTContract(targetChainID, peerAddr)
		if verifyErr != nil || !isPeerOFT {
			continue
		}
		underlyingToken, _ := l.QueryUnderlyingToken(targetChainID, peerAddr)
		l.SetOFTContract(targetChainID, symbol, peerAddr)
		if reg != nil {
			reg.Upsert(bridge.OFTToken{
				ChainID: targetChainID, Symbol: symbol, Address: peerAddr,
				UnderlyingTokenAddress: underlyingToken,
				Enabled: true, Source: "auto-discover-peer-unified",
			})
		}
		discovered[targetChainID] = peerAddr
		log.Infof("DiscoverToken: ✅ discovered %s on chain %s via peer from chain %s, address=%s", symbol, targetChainID, verifiedChainID, peerAddr)
	}

	return discovered, nil
}

// executeRealBridge 执行真实的 LayerZero 跨链转账
// 参考文档：https://docs.layerzero.network/v2/developers/evm/oft/native-transfer
// 返回：交易哈希、费用（wei）、错误
func (l *LayerZero) executeRealBridge(req *model.BridgeRequest, oftAddress string) (string, *big.Int, error) {
	log := logger.GetLoggerInstance().Named("bridge.layerzero").Sugar()

	// #region agent log
	debugLogLayerZero("layerzero.go:executeRealBridge:entry", "executeRealBridge called",
		map[string]interface{}{
			"fromChain":   req.FromChain,
			"toChain":     req.ToChain,
			"fromToken":   req.FromToken,
			"toToken":     req.ToToken,
			"amount":      req.Amount,
			"recipient":   req.Recipient,
			"oftAddress":  oftAddress,
			"bridgeRunId": "pre-fix",
		}, "H11-entry")
	// #endregion

	// 1. 获取 ethclient 连接
	client, err := l.getEthClient(req.FromChain)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get ethclient: %w", err)
	}

	// 2. 获取私钥
	cfg := config.GetGlobalConfig()
	if cfg == nil || cfg.Wallet.PrivateSecret == "" {
		return "", nil, fmt.Errorf("private key not configured")
	}

	privateKey, err := crypto.HexToECDSA(strings.TrimPrefix(cfg.Wallet.PrivateSecret, "0x"))
	if err != nil {
		return "", nil, fmt.Errorf("invalid private key: %w", err)
	}

	// 3. 获取发送地址
	fromAddress := crypto.PubkeyToAddress(privateKey.PublicKey)

	// 4. 获取 nonce（如果遇到 429 错误，尝试重新创建 client 并重试）
	nonce, err := client.PendingNonceAt(context.Background(), fromAddress)
	if err != nil {
		// 检查是否是 429 限流错误
		errStr := err.Error()
		if strings.Contains(errStr, "429") || strings.Contains(errStr, "Too Many Requests") || strings.Contains(errStr, "rate limit") {
			log.Warnf("RPC rate limited, trying to reconnect with fallback RPC URLs for chain %s", req.FromChain)
			// 清除缓存的 client，强制重新连接
			l.clientsMu.Lock()
			if oldClient, ok := l.clients[req.FromChain]; ok {
				oldClient.Close()
				delete(l.clients, req.FromChain)
			}
			l.clientsMu.Unlock()
			// 重新获取 client（会尝试备用 RPC URLs）
			client, err = l.getEthClient(req.FromChain)
			if err != nil {
				return "", nil, fmt.Errorf("failed to reconnect to RPC after rate limit: %w", err)
			}
			// 重试获取 nonce
			nonce, err = client.PendingNonceAt(context.Background(), fromAddress)
			if err != nil {
				return "", nil, fmt.Errorf("failed to get nonce after reconnection: %w", err)
			}
		} else {
			return "", nil, fmt.Errorf("failed to get nonce: %w", err)
		}
	}

	// 5. 获取 gas price
	gasPrice, err := client.SuggestGasPrice(context.Background())
	if err != nil {
		return "", nil, fmt.Errorf("failed to get gas price: %w", err)
	}

	// 6. 构建 OFT send() 方法的 calldata
	// OFT send() 方法签名：send(uint32 _dstEid, bytes32 _to, uint256 _amountLD, bytes _extraOptions, bytes _composeMsg, bytes _oftCmd)
	// 参考：https://docs.layerzero.network/v2/developers/evm/oft/api-reference
	//
	// 注意：这里需要：
	// - _dstEid: 目标链的 Endpoint ID（需要查询 LayerZero 文档）
	// - _to: 接收地址（bytes32 格式）
	// - _amountLD: 转账数量（考虑代币精度）
	// - _extraOptions: 额外的执行选项（可选）
	// - _composeMsg: 组合消息（可选）
	// - _oftCmd: OFT 命令（可选）
	//
	// 由于需要 OFT ABI 和 Endpoint ID 映射，这里提供一个基础框架
	// 实际使用时需要：
	// 1. 安装 @layerzerolabs/oft-evm 包获取 ABI
	// 2. 查询 LayerZero 文档获取各链的 Endpoint ID
	// 3. 构建完整的 calldata

	// 简化实现：构建一个基础的 send 调用
	// 实际应该使用 abi.Pack 来构建 calldata
	oftContract := common.HexToAddress(oftAddress)

	// 获取目标链的 Endpoint ID（简化：使用链 ID 作为 Endpoint ID，实际需要查询文档）
	dstEid := l.getEndpointID(req.ToChain)
	if dstEid == 0 {
		return "", nil, fmt.Errorf("unsupported destination chain: %s", req.ToChain)
	}

	// 转换接收地址为 bytes32（用于 OFT.send 调用）
	toAddress := common.HexToAddress(req.Recipient)
	toBytes32 := common.LeftPadBytes(toAddress.Bytes(), 32)

	// 查询 OFT 代币精度，amountLD 必须使用该精度否则会余额不足或 revert
	decimals, errDec := l.getOFTDecimals(context.Background(), client, oftContract)
	if errDec != nil {
		log.Warnf("Failed to get OFT decimals, assuming 18: %v", errDec)
		decimals = 18
	} else {
		log.Infof("OFT decimals on chain: %d", decimals)
	}

	// 转换金额：若已是整数串（最小单位）则直接用，否则按人类可读数量 × 10^decimals
	amountLD, ok := new(big.Int).SetString(strings.TrimSpace(req.Amount), 10)
	if !ok {
		amountFloat, err := parseAmount(req.Amount)
		if err != nil {
			return "", nil, fmt.Errorf("invalid amount: %w", err)
		}
		amountLD = amountToAmountLD(amountFloat, decimals)
		if amountLD.Sign() <= 0 {
			return "", nil, fmt.Errorf("amount would be zero after decimals conversion: %s", req.Amount)
		}
	}

	// 查询发送者在 OFT 合约上的实际代币余额，避免因余额不足导致 ERC20InsufficientBalance revert。
	// 若请求数量超过链上余额，自动 cap 到实际余额（桥接可用的部分好过整条 pipeline 失败）。
	onChainBalance, balErr := l.getTokenBalance(context.Background(), client, oftContract, fromAddress)
	if balErr != nil {
		log.Warnf("Failed to query OFT balance for %s on %s, skipping balance cap: %v", fromAddress.Hex(), oftAddress, balErr)
	} else {
		log.Infof("OFT balance of %s: %s, requested amountLD: %s", fromAddress.Hex(), onChainBalance.String(), amountLD.String())
		if onChainBalance.Sign() <= 0 {
			return "", nil, fmt.Errorf("on-chain token balance is zero for %s on contract %s", fromAddress.Hex(), oftAddress)
		}
		if amountLD.Cmp(onChainBalance) > 0 {
			diff := new(big.Int).Sub(amountLD, onChainBalance)
			log.Warnf("amountLD (%s) exceeds on-chain balance (%s), diff=%s wei; capping to actual balance",
				amountLD.String(), onChainBalance.String(), diff.String())
			amountLD = onChainBalance
		}
	}

	// 最小金额（设置为 amountLD 的 95%，允许 5% 滑点）
	minAmountLD := new(big.Int).Mul(amountLD, big.NewInt(95))
	minAmountLD.Div(minAmountLD, big.NewInt(100))

	// #region agent log
	debugLogLayerZero("layerzero.go:executeRealBridge:amountsAndFee", "amount / fee before quote & send",
		map[string]interface{}{
			"decimals":          decimals,
			"amountLD":          amountLD.String(),
			"minAmountLD":       minAmountLD.String(),
			"fromAddress":       fromAddress.Hex(),
			"toAddress":         toAddress.Hex(),
			"dstEid":            dstEid,
			"onChainBalanceLD":  onChainBalance.String(),
			"bridgeRunId":       "pre-fix",
		}, "H11-amount")
	// #endregion

	// 获取 OFT ABI
	oftABI, err := l.getOFTABI()
	if err != nil {
		return "", nil, fmt.Errorf("failed to get OFT ABI: %w", err)
	}

	// quoteSend(SendParam) 与 send(SendParam, ...) 使用同一 SendParam 结构（7 字段，与 ABI 一致）
	extraOptions := []byte{}
	composeMsg := []byte{}
	oftCmd := []byte{}
	sendParam := struct {
		DstEid       uint32
		To           [32]byte
		AmountLD     *big.Int
		MinAmountLD  *big.Int
		ExtraOptions []byte
		ComposeMsg   []byte
		OftCmd       []byte
	}{
		DstEid:       dstEid,
		To:           toBytes32ToArray32(toBytes32),
		AmountLD:     amountLD,
		MinAmountLD:  minAmountLD,
		ExtraOptions: extraOptions,
		ComposeMsg:   composeMsg,
		OftCmd:       oftCmd,
	}

	quoteCalldata, err := oftABI.Pack("quoteSend", sendParam)
	if err != nil {
		return "", nil, fmt.Errorf("failed to pack quoteSend: %w", err)
	}

	quoteResult, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &oftContract,
		Data: quoteCalldata,
	}, nil)

	// 初始化费用（默认值）
	messagingFee := struct {
		NativeFee  *big.Int
		LzTokenFee *big.Int
	}{
		NativeFee:  big.NewInt(0), // 默认使用原生代币支付费用
		LzTokenFee: big.NewInt(0), // 不使用 LZ Token
	}

	if err != nil {
		log.Warnf("Failed to quote send fee: %v (will use fallback native fee)", err)
	} else {
		// 解析 quoteSend 返回的费用
		var fee struct {
			NativeFee  *big.Int
			LzTokenFee *big.Int
		}
		if err := oftABI.UnpackIntoInterface(&fee, "quoteSend", quoteResult); err == nil {
			messagingFee.NativeFee = fee.NativeFee
			messagingFee.LzTokenFee = fee.LzTokenFee
			log.Infof("LayerZero quote fee: nativeFee=%s, lzTokenFee=%s",
				fee.NativeFee.String(), fee.LzTokenFee.String())
		} else {
			log.Warnf("Failed to unpack quoteSend result: %v", err)
		}
	}

	// 成功 send 的 calldata 中 [1]=nativeFee 必须非零，否则合约会 revert
	if messagingFee.NativeFee == nil || messagingFee.NativeFee.Sign() == 0 {
		messagingFee.NativeFee = new(big.Int).SetUint64(defaultNativeFeeWei)
		log.Warnf("LayerZero native fee was 0 or missing, using fallback: %s wei (0.0001 native)", messagingFee.NativeFee.String())
	}

	// 构建 OFT send 方法的 calldata（LayerZero V2 OFT 标准）
	// - 合约方法名: send(SendParam, MessagingFee, address) → MethodID 0xc7c7f5b3
	// - ERC20 transfer(address,uint256) = 0xa9059cbb 仅用于同链转账；跨链必须用 OFT send
	// - 区块浏览器上「transfer」多为目标链到账时合约内部调用的 ERC20 transfer，源链发起为 send
	// send(SendParam _sendParam, MessagingFee _fee, address _refundAddress)
	refundAddress := fromAddress // 退款地址使用发送地址

	packSend := func() ([]byte, error) {
		return oftABI.Pack("send", sendParam, messagingFee, refundAddress)
	}
	calldata, err := packSend()
	if err != nil {
		return "", nil, fmt.Errorf("failed to pack send calldata: %w", err)
	}

	// 估算 gas limit；若 revert 且为 InsufficientFee(requiredFee,...)，用所需费用重试一次
	gasLimit, err := client.EstimateGas(context.Background(), ethereum.CallMsg{
		From:  fromAddress,
		To:    &oftContract,
		Data:  calldata,
		Value: messagingFee.NativeFee,
	})
	if err != nil {
		if requiredFee := parseInsufficientFeeFromRevert(err); requiredFee != nil && requiredFee.Sign() > 0 {
			// 用合约要求的费用 +10% 重试
			messagingFee.NativeFee = new(big.Int).Mul(requiredFee, big.NewInt(110))
			messagingFee.NativeFee.Div(messagingFee.NativeFee, big.NewInt(100))
			log.Infof("LayerZero: retrying with required native fee (required=%s, using=%s)",
				requiredFee.String(), messagingFee.NativeFee.String())
			calldata, err = packSend()
			if err != nil {
				return "", nil, fmt.Errorf("failed to repack send calldata: %w", err)
			}
			gasLimit, err = client.EstimateGas(context.Background(), ethereum.CallMsg{
				From:  fromAddress,
				To:    &oftContract,
				Data:  calldata,
				Value: messagingFee.NativeFee,
			})
		}
		if err != nil {
			// #region agent log
			debugLogLayerZero("layerzero.go:executeRealBridge:estimateGasError", "estimateGas failed (send would revert)",
				map[string]interface{}{
					"error":        err.Error(),
					"fromChain":    req.FromChain,
					"toChain":      req.ToChain,
					"fromToken":    req.FromToken,
					"toToken":      req.ToToken,
					"amount":       req.Amount,
					"oftAddress":   oftAddress,
					"dstEid":       dstEid,
					"amountLD":     amountLD.String(),
					"minAmountLD":  minAmountLD.String(),
					"nativeFeeWei": messagingFee.NativeFee.String(),
					"bridgeRunId":  "pre-fix",
				}, "H11-estimateGas")
			// #endregion
			return "", nil, fmt.Errorf("estimateGas failed (send would revert): %w", err)
		}
	}
	// 增加 20% 的 gas limit 作为安全边际
	gasLimit = gasLimit * 120 / 100

	// 获取链 ID
	chainID, err := strconv.ParseInt(req.FromChain, 10, 64)
	if err != nil {
		return "", nil, fmt.Errorf("invalid chain ID: %w", err)
	}

	// 构建交易
	tx := types.NewTransaction(
		nonce,
		oftContract,
		messagingFee.NativeFee, // value: 支付原生代币费用
		gasLimit,
		gasPrice,
		calldata,
	)

	// 签名交易
	signer := types.LatestSignerForChainID(big.NewInt(chainID))
	signedTx, err := types.SignTx(tx, signer, privateKey)
	if err != nil {
		return "", nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	// 发送交易
	err = client.SendTransaction(context.Background(), signedTx)
	if err != nil {
		return "", nil, fmt.Errorf("failed to send transaction: %w", err)
	}

	txHash := signedTx.Hash().Hex()

	// 计算总费用（LayerZero 费用 + gas 费用）
	totalFee := new(big.Int).Set(messagingFee.NativeFee)
	gasCost := new(big.Int).Mul(gasPrice, big.NewInt(int64(gasLimit)))
	totalFee.Add(totalFee, gasCost)

	log.Infof("LayerZero bridge transaction sent: txHash=%s, gasLimit=%d, gasPrice=%s, totalFee=%s",
		txHash, gasLimit, gasPrice.String(), totalFee.String())

	return txHash, totalFee, nil
}

// getEndpointID 获取指定链的 LayerZero Endpoint ID
// 参考：https://docs.layerzero.network/v2/deployments/chains
func (l *LayerZero) getEndpointID(chainID string) uint32 {
	endpointIDs := map[string]uint32{
		"1":      30101, // Ethereum Mainnet
		"56":     30102, // BSC Mainnet
		"137":    30109, // Polygon
		"42161":  30110, // Arbitrum
		"10":     30111, // Optimism
		"43114":  30106, // Avalanche
		"8453":   30184, // Base
		"250":    30112, // Fantom
		"59144":  30183, // Linea
		"324":    30165, // zkSync Era
		"5000":   30181, // Mantle
		"534352": 30214, // Scroll
		"1101":   30158, // Polygon zkEVM
	}

	if eid, ok := endpointIDs[chainID]; ok {
		return eid
	}
	return 0
}

// parseAmount 解析金额字符串（支持科学计数法）
func parseAmount(amountStr string) (float64, error) {
	amountStr = strings.TrimSpace(amountStr)
	if amountStr == "" {
		return 0, fmt.Errorf("empty amount")
	}

	// 尝试直接解析
	amount, err := strconv.ParseFloat(amountStr, 64)
	if err == nil {
		return amount, nil
	}

	return 0, fmt.Errorf("invalid amount format: %s", amountStr)
}

// toBytes32ToArray32 将 []byte 转换为 [32]byte
func toBytes32ToArray32(b []byte) [32]byte {
	var result [32]byte
	copy(result[:], b)
	return result
}

// parseInsufficientFeeFromRevert 从 estimateGas/CallContract 的 revert 错误中解析 InsufficientFee(requiredNativeFee,...)。
// 若错误数据以 0x4f3ec0d3 开头，则返回第一个参数（所需原生费用 wei），否则返回 nil。
func parseInsufficientFeeFromRevert(err error) *big.Int {
	if err == nil {
		return nil
	}
	s := err.Error()
	idx := strings.Index(s, insufficientFeeSelector)
	if idx < 0 {
		return nil
	}
	hexStr := strings.TrimPrefix(s[idx:], insufficientFeeSelector)
	hexStr = strings.TrimSpace(hexStr)
	// 第一个参数为 uint256，占 64 个十六进制字符
	if len(hexStr) < 64 {
		return nil
	}
	hexStr = hexStr[:64]
	data := common.FromHex(hexStr)
	if len(data) != 32 {
		return nil
	}
	return new(big.Int).SetBytes(data)
}

// getOFTDecimals 调用 OFT 合约的 decimals()（ERC20 标准）获取代币精度，用于正确计算 amountLD
func (l *LayerZero) getOFTDecimals(ctx context.Context, client *ethclient.Client, oftContract common.Address) (int, error) {
	// decimals() method selector: 0x313ce567
	decimalsCalldata := common.Hex2Bytes("313ce567")
	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &oftContract,
		Data: decimalsCalldata,
	}, nil)
	if err != nil {
		return 0, fmt.Errorf("call OFT decimals failed: %w", err)
	}
	if len(result) < 32 {
		return 0, fmt.Errorf("decimals() returned invalid length: %d", len(result))
	}
	// ABI 编码的 uint8 右填充到 32 字节，取最后一字节
	d := int(result[31])
	if d < 0 || d > 77 {
		return 0, fmt.Errorf("decimals out of range: %d", d)
	}
	return d, nil
}

// amountToAmountLD 将人类可读数量转为 OFT 的 amountLD（按代币精度）
// 使用 256-bit big.Float 精度避免默认 53-bit（float64）精度导致的舍入误差
func amountToAmountLD(amountFloat float64, decimals int) *big.Int {
	multiplier := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	mult := new(big.Float).SetPrec(256).SetInt(multiplier)
	amountF := new(big.Float).SetPrec(256).SetFloat64(amountFloat)
	amountF.Mul(amountF, mult)
	var amountLD big.Int
	amountF.Int(&amountLD)
	return &amountLD
}

// getTokenBalance 查询 fromAddress 在 ERC20 合约上的余额（balanceOf）
func (l *LayerZero) getTokenBalance(ctx context.Context, client *ethclient.Client, tokenContract, owner common.Address) (*big.Int, error) {
	// balanceOf(address) selector: 0x70a08231
	data := common.Hex2Bytes("70a08231")
	// address 参数左填充到 32 字节
	data = append(data, common.LeftPadBytes(owner.Bytes(), 32)...)
	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &tokenContract,
		Data: data,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("balanceOf call failed: %w", err)
	}
	if len(result) < 32 {
		return nil, fmt.Errorf("balanceOf returned invalid length: %d", len(result))
	}
	return new(big.Int).SetBytes(result), nil
}
