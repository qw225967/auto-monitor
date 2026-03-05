package pipeline

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/onchain"
	"github.com/qw225967/auto-monitor/internal/utils/logger"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// OnchainNodeConfig 链上节点配置
type OnchainNodeConfig struct {
	// 节点基础信息
	ID   string
	Name string

	// 链信息
	ChainID string // 链 ID（如 "1" ETH, "56" BSC）

	// 资产信息
	AssetSymbol  string // 资产符号（如 "USDT"）
	TokenAddress string // 代币合约地址（如 USDT 合约）

	// 入金地址配置
	WalletAddress string // 本节点用于接收资产的钱包地址

	// 客户端实例
	Client onchain.OnchainClient
}

// OnchainNode 链上节点实现
type OnchainNode struct {
	cfg OnchainNodeConfig
}

// NewOnchainNode 创建链上节点
func NewOnchainNode(cfg OnchainNodeConfig) *OnchainNode {
	if cfg.ID == "" {
		cfg.ID = fmt.Sprintf("onchain-%s-%s", cfg.ChainID, cfg.AssetSymbol)
	}
	if cfg.Name == "" {
		cfg.Name = cfg.ID
	}
	return &OnchainNode{cfg: cfg}
}

// --- Node 接口实现 ---

func (n *OnchainNode) GetID() string {
	return n.cfg.ID
}

func (n *OnchainNode) GetName() string {
	return n.cfg.Name
}

// GetChainID 返回链 ID（如 "1", "56"）
func (n *OnchainNode) GetChainID() string {
	return n.cfg.ChainID
}

// GetTokenAddress 返回该节点的代币合约地址
func (n *OnchainNode) GetTokenAddress() string {
	return n.cfg.TokenAddress
}

// SetTokenAddress 更新该节点的代币合约地址（用于 OFT 发现后修正地址）
func (n *OnchainNode) SetTokenAddress(addr string) {
	n.cfg.TokenAddress = addr
}

func (n *OnchainNode) GetType() NodeType {
	return NodeTypeOnchain
}

func (n *OnchainNode) GetAsset() string {
	return n.cfg.AssetSymbol
}

// CheckBalance 检查链上钱包是否持有目标资产
func (n *OnchainNode) CheckBalance() (float64, error) {
	if n.cfg.Client == nil {
		return 0, fmt.Errorf("onchain client is nil")
	}

	// 这里使用 Okex DEX 聚合接口返回的资产列表，简单聚合同一资产的余额
	if n.cfg.WalletAddress == "" || n.cfg.ChainID == "" {
		return 0, fmt.Errorf("walletAddress or chainID is empty")
	}

	assets, err := n.cfg.Client.GetAllTokenBalances(n.cfg.WalletAddress, n.cfg.ChainID, false)
	if err != nil {
		return 0, fmt.Errorf("GetAllTokenBalances failed: %w", err)
	}

	var total float64
	for _, a := range assets {
		if a.Symbol == n.cfg.AssetSymbol {
			// 这里只做简单解析，真实场景需要考虑精度
			val, parseErr := parseStringToFloat(a.Balance)
			if parseErr != nil {
				continue
			}
			total += val
		}
	}
	return total, nil
}

// GetAvailableBalance 返回可用余额（链上视为全部可用）
func (n *OnchainNode) GetAvailableBalance() (float64, error) {
	return n.CheckBalance()
}

// CanDeposit 链上钱包总是可以接收转入
// 增强版本：检查钱包地址和客户端是否配置并可用
func (n *OnchainNode) CanDeposit() bool {
	// 检查钱包地址是否配置
	if n.cfg.WalletAddress == "" {
		return false
	}

	// 检查客户端是否配置
	if n.cfg.Client == nil {
		return false
	}

	// 检查链ID是否配置
	if n.cfg.ChainID == "" {
		return false
	}

	return true
}

// CheckDepositAvailability 检查充币可用性（检查钱包地址和客户端）
func (n *OnchainNode) CheckDepositAvailability(network string) (bool, error) {
	// 基础检查
	if !n.CanDeposit() {
		return false, fmt.Errorf("basic deposit check failed: wallet address, client, or chain ID not configured")
	}

	// 验证客户端是否可用（通过尝试查询余额）
	if n.cfg.Client != nil {
		_, err := n.cfg.Client.GetAllTokenBalances(n.cfg.WalletAddress, n.cfg.ChainID, false)
		if err != nil {
			return false, fmt.Errorf("client not available or chain not supported: %w", err)
		}
	}

	return true, nil
}

// GetDepositAddress 返回链上钱包地址（asset 仅保留语义，链上同一钱包接收任意代币）
func (n *OnchainNode) GetDepositAddress(asset, network string) (*model.DepositAddress, error) {
	if !n.CanDeposit() {
		return nil, fmt.Errorf("onchain node not configured for deposit")
	}
	return &model.DepositAddress{
		Asset:   n.cfg.AssetSymbol,
		Address: n.cfg.WalletAddress,
		Network: n.cfg.ChainID,
		Memo:    "",
	}, nil
}

// CheckDepositStatus 链上充值视为直接到账，真正的确认由上层通过 GetTxResult 控制
// 这里实现为简单返回 true，供执行引擎按需扩展。
func (n *OnchainNode) CheckDepositStatus(txHash string) (bool, error) {
	// 由 Pipeline 统一通过 OnchainClient.GetTxResult 做更细粒度的确认
	return true, nil
}

// CanWithdraw 链上节点可提币到地址时返回 true（配置完整）；跨链由边触发，由 Pipeline 的 bridgeManager 执行
func (n *OnchainNode) CanWithdraw() bool {
	return n.cfg.WalletAddress != "" && n.cfg.Client != nil && n.cfg.ChainID != ""
}

// CheckWithdrawAvailability 检查提币可用性（链上节点不直接支持提币）
func (n *OnchainNode) CheckWithdrawAvailability(network string) (bool, error) {
	return false, fmt.Errorf("onchain node does not support direct withdraw; cross-chain is edge behavior, use Pipeline.SetBridgeManager for chain-to-chain edges")
}

// Withdraw 从链上节点转账到目标地址（用于充值到交易所等场景）
func (n *OnchainNode) Withdraw(amount float64, toAddress string, network string, memo string) (*model.WithdrawResponse, error) {
	log := logger.GetLoggerInstance().Named("pipeline.onchain").Sugar()
	
	if n.cfg.Client == nil {
		return nil, fmt.Errorf("onchain client is nil")
	}
	if n.cfg.WalletAddress == "" {
		return nil, fmt.Errorf("wallet address not configured")
	}
	if n.cfg.ChainID == "" {
		return nil, fmt.Errorf("chain ID not configured")
	}
	if n.cfg.TokenAddress == "" {
		return nil, fmt.Errorf("token address not configured")
	}
	if toAddress == "" {
		return nil, fmt.Errorf("to address is empty")
	}

	// 获取私钥
	cfg := config.GetGlobalConfig()
	if cfg == nil || cfg.Wallet.PrivateSecret == "" {
		return nil, fmt.Errorf("private key not configured")
	}

	privateKey, err := crypto.HexToECDSA(strings.TrimPrefix(cfg.Wallet.PrivateSecret, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	fromAddress := crypto.PubkeyToAddress(privateKey.PublicKey)
	if !strings.EqualFold(fromAddress.Hex(), n.cfg.WalletAddress) {
		return nil, fmt.Errorf("wallet address mismatch: configured=%s, derived=%s", n.cfg.WalletAddress, fromAddress.Hex())
	}

	// 获取 RPC 客户端（通过 OnchainClient 获取，或使用默认 RPC）
	chainID, err := strconv.ParseInt(n.cfg.ChainID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid chain ID: %s", n.cfg.ChainID)
	}

	// 获取 RPC URL（优先从配置获取，否则使用常量中的默认值）
	globalConfig := config.GetGlobalConfig()
	var rpcURL string
	if globalConfig != nil && globalConfig.Bridge.LayerZero.RPCURLs != nil {
		if url, ok := globalConfig.Bridge.LayerZero.RPCURLs[n.cfg.ChainID]; ok && url != "" {
			rpcURL = url
		}
	}
	if rpcURL == "" {
		rpcURL = constants.GetDefaultRPCURL(n.cfg.ChainID)
	}
	if rpcURL == "" {
		return nil, fmt.Errorf("RPC URL not configured for chain %s", n.cfg.ChainID)
	}

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RPC: %w", err)
	}
	defer client.Close()

	// 获取 nonce
	nonce, err := client.PendingNonceAt(context.Background(), fromAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}

	// 获取 gas price
	gasPrice, err := client.SuggestGasPrice(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get gas price: %w", err)
	}

	// 构建 ERC20 transfer 调用
	// transfer(address to, uint256 amount) -> 0xa9059cbb
	tokenAddress := common.HexToAddress(n.cfg.TokenAddress)
	toAddr := common.HexToAddress(toAddress)
	// #region agent log
	debugLogAgent("onchain_node:Withdraw:tokenAddr", "using token address for ERC-20 ops", map[string]interface{}{
		"nodeID": n.cfg.ID, "chainID": n.cfg.ChainID, "asset": n.cfg.AssetSymbol,
		"tokenAddress": n.cfg.TokenAddress, "amount": amount, "toAddress": toAddress,
	}, "H2-node")
	// #endregion

	// 查询代币精度（decimals）
	decimals := 18 // 默认值
	decimalsCalldata := common.FromHex("0x313ce567") // decimals() method selector
	decimalsResult, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &tokenAddress,
		Data: decimalsCalldata,
	}, nil)
	if err == nil && len(decimalsResult) >= 32 {
		decimals = int(new(big.Int).SetBytes(decimalsResult[24:32]).Uint64()) // 取最后8字节（uint8右填充到32字节）
		log.Debugf("Token decimals queried: %d", decimals)
	} else {
		log.Warnf("Failed to query token decimals, using default 18: %v", err)
	}

	// 转换金额：amount * 10^decimals
	// 使用字符串转换避免浮点数精度问题
	amountStr := fmt.Sprintf("%.18f", amount) // 使用足够的小数位数
	amountFloat, _, err := big.ParseFloat(amountStr, 10, 256, big.ToNearestEven)
	if err != nil {
		return nil, fmt.Errorf("failed to parse amount: %w", err)
	}
	
	decimalsInt := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	decimalsFloat := new(big.Float).SetInt(decimalsInt)
	
	amountFloat.Mul(amountFloat, decimalsFloat)
	amountInt, _ := amountFloat.Int(nil)
	
	// 验证转换结果
	if amountInt == nil {
		return nil, fmt.Errorf("failed to convert amount to integer: amount=%.18f, decimals=%d", amount, decimals)
	}
	
	// 直接通过 RPC 查询链上实际余额（而不是使用聚合接口）
	balanceABI, err := abi.JSON(strings.NewReader(`[{"constant":true,"inputs":[{"name":"_owner","type":"address"}],"name":"balanceOf","outputs":[{"name":"balance","type":"uint256"}],"type":"function"}]`))
	if err != nil {
		return nil, fmt.Errorf("failed to parse balanceOf ABI: %w", err)
	}
	
	balanceData, err := balanceABI.Pack("balanceOf", fromAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to pack balanceOf calldata: %w", err)
	}
	
	balanceResult, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &tokenAddress,
		Data: balanceData,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to query token balance from chain: %w", err)
	}
	
	if len(balanceResult) < 32 {
		return nil, fmt.Errorf("invalid balance query result length: %d", len(balanceResult))
	}
	
	// 获取链上实际余额（原始值，未考虑精度）
	onchainBalanceInt := new(big.Int).SetBytes(balanceResult)
	
	// 将链上余额转换为人类可读格式
	balanceFloat := new(big.Float).SetInt(onchainBalanceInt)
	balanceFloat.Quo(balanceFloat, decimalsFloat)
	balanceHuman, _ := balanceFloat.Float64()
	
	log.Infof("On-chain balance check: raw=%s, human-readable=%.8f, decimals=%d, required amount=%.8f (raw=%s)", 
		onchainBalanceInt.String(), balanceHuman, decimals, amount, amountInt.String())
	
	// 检查余额是否充足（直接比较原始值）
	if amountInt.Cmp(onchainBalanceInt) > 0 {
		return nil, fmt.Errorf("insufficient token balance for deposit to exchange: required=%.8f (raw=%s, decimals=%d), available on-chain=%.8f (raw=%s, decimals=%d). Token: %s, From: %s, To: %s", 
			amount, amountInt.String(), decimals, balanceHuman, onchainBalanceInt.String(), decimals, n.cfg.TokenAddress, fromAddress.Hex(), toAddress)
	}
	
	log.Infof("Balance check passed: required=%.8f, available on-chain=%.8f", amount, balanceHuman)

	// 构建 transfer 方法的 calldata
	// transfer(address,uint256) 方法签名
	transferABI, err := abi.JSON(strings.NewReader(`[{"constant":false,"inputs":[{"name":"_to","type":"address"},{"name":"_value","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"type":"function"}]`))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %w", err)
	}

	calldata, err := transferABI.Pack("transfer", toAddr, amountInt)
	if err != nil {
		return nil, fmt.Errorf("failed to pack transfer calldata: %w", err)
	}

	// 估算 gas limit
	gasLimit, err := client.EstimateGas(context.Background(), ethereum.CallMsg{
		From: fromAddress,
		To:   &tokenAddress,
		Data: calldata,
	})
	if err != nil {
		// 尝试解析错误信息，提供更详细的错误提示
		errStr := err.Error()
		if strings.Contains(errStr, "execution reverted") {
			// 提取错误代码和数据
			if strings.Contains(errStr, "0xe450d38c") {
				// 这可能是自定义错误，尝试解析错误数据
				log.Errorf("ERC20 transfer failed with custom error 0xe450d38c. Amount: %s (%.8f), Token: %s, From: %s, To: %s", 
					amountInt.String(), amount, n.cfg.TokenAddress, fromAddress.Hex(), toAddress)
				// 检查目标地址是否是交易所地址（通常以特定格式或已知地址）
				return nil, fmt.Errorf("ERC20 transfer failed (deposit to exchange): insufficient balance or transfer limit exceeded. Token: %s, Amount: %.8f (decimals: %d), On-chain balance: %s. Please check token balance and transfer restrictions", 
					n.cfg.TokenAddress, amount, decimals, onchainBalanceInt.String())
			}
		}
		return nil, fmt.Errorf("failed to estimate gas for ERC20 transfer (deposit to exchange): %w. Token: %s, Amount: %.8f (decimals: %d), From: %s, To: %s, On-chain balance: %s", 
			err, n.cfg.TokenAddress, amount, decimals, fromAddress.Hex(), toAddress, onchainBalanceInt.String())
	}
	// 增加 20% 的安全边际
	gasLimit = gasLimit * 120 / 100

	// 构建交易
	tx := types.NewTransaction(
		nonce,
		tokenAddress,
		big.NewInt(0), // value = 0 (ERC20 transfer)
		gasLimit,
		gasPrice,
		calldata,
	)

	// 签名交易
	signer := types.NewEIP155Signer(big.NewInt(chainID))
	signedTx, err := types.SignTx(tx, signer, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	// 发送交易
	err = client.SendTransaction(context.Background(), signedTx)
	if err != nil {
		return nil, fmt.Errorf("failed to send transaction: %w", err)
	}

	txHash := signedTx.Hash().Hex()
	log.Infof("ERC20 transfer sent: from=%s, to=%s, token=%s, amount=%.8f, txHash=%s",
		fromAddress.Hex(), toAddress, n.cfg.TokenAddress, amount, txHash)

	return &model.WithdrawResponse{
		WithdrawID: txHash,
		Status:     "PENDING",
		TxHash:     txHash,
		CreateTime: time.Now(),
	}, nil
}

// CheckWithdrawStatus 检查链上转账交易的确认状态
// withdrawID 是交易哈希（txHash）
func (n *OnchainNode) CheckWithdrawStatus(withdrawID string) (bool, error) {
	if withdrawID == "" {
		return false, fmt.Errorf("withdrawID (txHash) is empty")
	}

	log := logger.GetLoggerInstance().Named("pipeline.onchain").Sugar()

	// 获取 RPC URL（优先从配置获取，否则使用常量中的默认值）
	globalConfig := config.GetGlobalConfig()
	var rpcURL string
	if globalConfig != nil && globalConfig.Bridge.LayerZero.RPCURLs != nil {
		if url, ok := globalConfig.Bridge.LayerZero.RPCURLs[n.cfg.ChainID]; ok && url != "" {
			rpcURL = url
		}
	}
	if rpcURL == "" {
		rpcURL = constants.GetDefaultRPCURL(n.cfg.ChainID)
	}
	if rpcURL == "" {
		return false, fmt.Errorf("RPC URL not configured for chain %s", n.cfg.ChainID)
	}

	// 连接 RPC 客户端
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return false, fmt.Errorf("failed to connect to RPC: %w", err)
	}
	defer client.Close()

	// 查询交易回执
	txHash := common.HexToHash(withdrawID)
	receipt, err := client.TransactionReceipt(context.Background(), txHash)
	if err != nil {
		// 交易可能还未确认（还未被打包或还在 pending 状态）
		log.Debugf("Transaction receipt not found (may be pending): txHash=%s, error=%v", withdrawID, err)
		return false, nil // 返回 false, nil 表示等待状态，让调用方继续轮询
	}

	// 检查交易状态
	if receipt.Status == 0 {
		// 交易失败（reverted）
		log.Warnf("Transaction failed (reverted): txHash=%s", withdrawID)
		return false, fmt.Errorf("transaction failed (reverted): txHash=%s", withdrawID)
	}

	// 交易成功确认
	log.Infof("Transaction confirmed successfully: txHash=%s, blockNumber=%d", withdrawID, receipt.BlockNumber.Uint64())
	return true, nil
}

// getRPCClient 获取链上 RPC 客户端（调用方负责 Close）
func (n *OnchainNode) getRPCClient() (*ethclient.Client, error) {
	globalConfig := config.GetGlobalConfig()
	var rpcURL string
	if globalConfig != nil && globalConfig.Bridge.LayerZero.RPCURLs != nil {
		if url, ok := globalConfig.Bridge.LayerZero.RPCURLs[n.cfg.ChainID]; ok && url != "" {
			rpcURL = url
		}
	}
	if rpcURL == "" {
		rpcURL = constants.GetDefaultRPCURL(n.cfg.ChainID)
	}
	if rpcURL == "" {
		return nil, fmt.Errorf("RPC URL not configured for chain %s", n.cfg.ChainID)
	}
	return ethclient.Dial(rpcURL)
}

// --- 辅助函数 ---

// parseStringToFloat 尝试将字符串解析为 float64（忽略解析错误）
func parseStringToFloat(s string) (float64, error) {
	if s == "" {
		return 0, nil
	}
	// 为避免在此处引入 big.Float 等复杂依赖，这里使用简单的 ParseFloat，
	// 精度要求较高的场景可以在后续迭代中改为使用大数库。
	return strconv.ParseFloat(s, 64)
}

