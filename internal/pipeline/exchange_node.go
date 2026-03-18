package pipeline

import (
	"fmt"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/internal/exchange"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/position"
	"github.com/qw225967/auto-monitor/internal/utils/logger"
)

// ExchangeNodeConfig 交易所节点配置
type ExchangeNodeConfig struct {
	// 节点基础信息
	ID   string // 节点唯一 ID（可选，不填则使用 ExchangeType+Asset）
	Name string // 人类可读名称（可选）

	// 交易所信息
	ExchangeType string // 交易所类型（如 "binance", "bybit", "bitget"）

	// 资产信息
	Asset          string // 资产符号（如 "USDT"）
	DefaultNetwork string // 默认提币网络（如 "ERC20", "TRC20", "BEP20"）
}

// ExchangeNode 交易所节点实现
// 内部通过 WalletManager 获取底层 Exchange，并在需要时断言为 DepositWithdrawProvider。
type ExchangeNode struct {
	cfg ExchangeNodeConfig

	// 底层交易所实例（可选缓存）
	ex exchange.DepositWithdrawProvider

	// 保护 cfg.Asset 的临时修改（SetAssetTemporary），防止 forward/backward Pipeline 并发冲突
	assetMu sync.Mutex

	// 可充提币检测缓存
	availabilityCache map[string]*availabilityCheckResult
	cacheMu           sync.RWMutex
}

// availabilityCheckResult 可充提币检测结果
type availabilityCheckResult struct {
	available bool
	err       error
	timestamp time.Time
}

// 缓存有效期：5分钟
const availabilityCacheTTL = 5 * time.Minute

// NewExchangeNode 创建交易所节点
func NewExchangeNode(cfg ExchangeNodeConfig) *ExchangeNode {
	// 如果未指定 ID，则使用 ExchangeType-Asset 作为默认 ID
	if cfg.ID == "" {
		cfg.ID = fmt.Sprintf("%s-%s", cfg.ExchangeType, cfg.Asset)
	}
	if cfg.Name == "" {
		cfg.Name = cfg.ID
	}
	return &ExchangeNode{
		cfg:               cfg,
		availabilityCache: make(map[string]*availabilityCheckResult),
	}
}

// ensureExchange 确保 ex 已经初始化（通过 WalletManager 获取）
func (n *ExchangeNode) ensureExchange() error {
	if n.ex != nil {
		return nil
	}

	wm := position.GetWalletManager()
	if wm == nil {
		return fmt.Errorf("wallet manager not initialized")
	}

	cex := wm.GetExchange(n.cfg.ExchangeType)
	if cex == nil {
		return fmt.Errorf("exchange %s not found in wallet manager", n.cfg.ExchangeType)
	}

	dw, ok := cex.(exchange.DepositWithdrawProvider)
	if !ok {
		return fmt.Errorf("exchange %s does not implement DepositWithdrawProvider", n.cfg.ExchangeType)
	}

	n.ex = dw
	return nil
}

// --- Node 接口实现 ---

func (n *ExchangeNode) GetID() string {
	return n.cfg.ID
}

func (n *ExchangeNode) GetName() string {
	return n.cfg.Name
}

func (n *ExchangeNode) GetType() NodeType {
	return NodeTypeExchange
}

func (n *ExchangeNode) GetAsset() string {
	n.assetMu.Lock()
	defer n.assetMu.Unlock()
	return n.cfg.Asset
}

// SetAssetTemporary 临时修改节点的资产名（用于 backward pipeline 查询 USDT 余额/提币等场景）
// 调用方必须在使用后恢复原值。并发安全：通过 assetMu 保护。
func (n *ExchangeNode) SetAssetTemporary(asset string) {
	n.assetMu.Lock()
	defer n.assetMu.Unlock()
	n.cfg.Asset = asset
}

// GetBalanceForAsset 查询指定资产的可用余额（不修改节点状态，并发安全）
func (n *ExchangeNode) GetBalanceForAsset(asset string) (float64, error) {
	if err := n.ensureExchange(); err != nil {
		return 0, err
	}
	balances, err := n.ex.GetAllBalances()
	if err != nil {
		return 0, fmt.Errorf("get all balances failed: %w", err)
	}
	if b, ok := balances[asset]; ok && b != nil {
		return b.Available, nil
	}
	return 0, nil
}

// GetSpotBalanceForAsset 查询现货账户中指定资产的可用余额。
// 搬砖（现货-现货）场景应使用此方法，避免合约余额干扰。
func (n *ExchangeNode) GetSpotBalanceForAsset(asset string) (float64, error) {
	if err := n.ensureExchange(); err != nil {
		return 0, err
	}
	balances, err := n.ex.GetSpotBalances()
	if err != nil {
		return n.GetBalanceForAsset(asset)
	}
	if b, ok := balances[asset]; ok && b != nil {
		return b.Available, nil
	}
	return 0, nil
}

// CheckBalance 检查账户是否持有目标资产
func (n *ExchangeNode) CheckBalance() (float64, error) {
	if err := n.ensureExchange(); err != nil {
		return 0, err
	}
	balances, err := n.ex.GetAllBalances()
	if err != nil {
		return 0, fmt.Errorf("get all balances failed: %w", err)
	}
	if b, ok := balances[n.cfg.Asset]; ok && b != nil {
		return b.Available, nil
	}
	return 0, nil
}

// GetAvailableBalance 返回可用余额
func (n *ExchangeNode) GetAvailableBalance() (float64, error) {
	return n.CheckBalance()
}

// CanDeposit 交易所一般都支持充币（前提是配置了该资产）
// 增强版本：检查基础配置和接口实现
func (n *ExchangeNode) CanDeposit() bool {
	// 基础检查：资产和交易所类型是否配置
	if n.cfg.Asset == "" || n.cfg.ExchangeType == "" {
		return false
	}

	// 检查是否实现了 DepositWithdrawProvider 接口
	if err := n.ensureExchange(); err != nil {
		return false
	}

	return true
}

// GetDepositAddress 按 symbol 向交易所查询充币地址；asset 为空时使用节点配置资产
func (n *ExchangeNode) GetDepositAddress(asset, network string) (*model.DepositAddress, error) {
	if err := n.ensureExchange(); err != nil {
		return nil, err
	}
	assetUsed := asset
	if assetUsed == "" {
		assetUsed = n.cfg.Asset
	}
	networkUsed := network
	if network == "" {
		networkUsed = n.cfg.DefaultNetwork
	}
	// #region agent log
	debugLogAgent("exchange_node:GetDepositAddress", "calling exchange Deposit by symbol", map[string]interface{}{
		"nodeID": n.GetID(), "assetParam": asset, "assetUsed": assetUsed, "networkParam": network, "networkUsed": networkUsed,
	}, "H-deposit-4018")
	// #endregion
	return n.ex.Deposit(assetUsed, networkUsed)
}

// CheckDepositStatus 基于充币记录检查是否到账（通过 txHash 匹配）
func (n *ExchangeNode) CheckDepositStatus(txHash string) (bool, error) {
	if txHash == "" {
		return false, fmt.Errorf("txHash is empty")
	}
	if err := n.ensureExchange(); err != nil {
		return false, err
	}

	records, err := n.ex.GetDepositHistory(n.cfg.Asset, 100)
	if err != nil {
		return false, fmt.Errorf("get deposit history failed: %w", err)
	}
	for _, r := range records {
		if r.TxHash == txHash {
			// 简单判断：非 PENDING 视为已完成
			return r.Status != "PENDING", nil
		}
	}
	return false, nil
}

// CanWithdraw 是否支持提币
// 增强版本：检查基础配置和接口实现
func (n *ExchangeNode) CanWithdraw() bool {
	if n.cfg.Asset == "" || n.cfg.ExchangeType == "" {
		return false
	}
	if err := n.ensureExchange(); err != nil {
		return false
	}
	return true
}

// ValidateDepositWithdrawCapability 验证交易所是否真正具备充提能力（非空壳）。
// DEX（如 Hyperliquid/Lighter/Aster）虽然实现了 DepositWithdrawProvider 接口，
// 但 Withdraw/Deposit 均返回错误。此方法通过 GetWithdrawNetworks 探测实际能力，
// 应在 Pipeline 创建时调用而非运行时，以提前拦截配置错误。
func (n *ExchangeNode) ValidateDepositWithdrawCapability() error {
	if err := n.ensureExchange(); err != nil {
		return fmt.Errorf("exchange %s init failed: %w", n.cfg.ExchangeType, err)
	}
	wnl, ok := n.ex.(exchange.WithdrawNetworkLister)
	if !ok {
		return fmt.Errorf("exchange %s does not implement WithdrawNetworkLister — likely a DEX without deposit/withdraw support", n.cfg.ExchangeType)
	}
	asset := n.cfg.Asset
	if asset == "" {
		asset = "USDT"
	}
	nets, err := wnl.GetWithdrawNetworks(asset)
	if err != nil {
		return fmt.Errorf("exchange %s does not support withdraw (GetWithdrawNetworks error: %w)", n.cfg.ExchangeType, err)
	}
	if len(nets) == 0 {
		return fmt.Errorf("exchange %s returned 0 withdraw networks for %s — likely a DEX that does not support traditional deposit/withdraw", n.cfg.ExchangeType, asset)
	}
	return nil
}

// CheckDepositAvailability 检查充币可用性（基础检查 + API 检查 + 网络检查）
func (n *ExchangeNode) CheckDepositAvailability(network string) (bool, error) {
	// 基础检查
	if !n.CanDeposit() {
		return false, fmt.Errorf("basic deposit check failed: asset or exchange type not configured")
	}

	// 使用默认网络（如果未指定）
	if network == "" {
		network = n.cfg.DefaultNetwork
	}

	// 检查缓存
	cacheKey := fmt.Sprintf("deposit:%s:%s", n.cfg.Asset, network)
	if result := n.getCachedAvailability(cacheKey); result != nil {
		return result.available, result.err
	}

	// API 检查：尝试获取充币地址
	available := true
	var checkErr error

	if err := n.ensureExchange(); err != nil {
		available = false
		checkErr = fmt.Errorf("exchange not available: %w", err)
	} else {
		// 尝试获取充币地址验证网络支持
		_, err := n.ex.Deposit(n.cfg.Asset, network)
		if err != nil {
			available = false
			checkErr = fmt.Errorf("deposit address check failed: %w", err)
		}
	}

	// 缓存结果
	n.setCachedAvailability(cacheKey, available, checkErr)

	return available, checkErr
}

// CheckWithdrawAvailability 检查提币可用性（基础检查 + API 检查 + 网络检查）
func (n *ExchangeNode) CheckWithdrawAvailability(network string) (bool, error) {
	// 基础检查
	if !n.CanWithdraw() {
		return false, fmt.Errorf("basic withdraw check failed: asset or exchange type not configured")
	}

	// 使用默认网络（如果未指定）
	if network == "" {
		network = n.cfg.DefaultNetwork
	}

	// 检查缓存
	cacheKey := fmt.Sprintf("withdraw:%s:%s", n.cfg.Asset, network)
	if result := n.getCachedAvailability(cacheKey); result != nil {
		return result.available, result.err
	}

	// API 检查：尝试查询提币历史（验证是否支持）
	available := true
	var checkErr error

	if err := n.ensureExchange(); err != nil {
		available = false
		checkErr = fmt.Errorf("exchange not available: %w", err)
	} else {
		// 尝试查询提币历史验证支持（只查询少量记录，不实际提币）
		_, err := n.ex.GetWithdrawHistory(n.cfg.Asset, 1)
		if err != nil {
			// 如果查询失败，可能是网络不支持或资产不支持提币
			available = false
			checkErr = fmt.Errorf("withdraw history check failed: %w", err)
		}
	}

	// 缓存结果
	n.setCachedAvailability(cacheKey, available, checkErr)

	return available, checkErr
}

// getCachedAvailability 获取缓存的可用性检查结果
func (n *ExchangeNode) getCachedAvailability(key string) *availabilityCheckResult {
	n.cacheMu.RLock()
	defer n.cacheMu.RUnlock()

	result, ok := n.availabilityCache[key]
	if !ok {
		return nil
	}

	// 检查缓存是否过期
	if time.Since(result.timestamp) > availabilityCacheTTL {
		return nil
	}

	return result
}

// setCachedAvailability 设置缓存的可用性检查结果
func (n *ExchangeNode) setCachedAvailability(key string, available bool, err error) {
	n.cacheMu.Lock()
	defer n.cacheMu.Unlock()

	n.availabilityCache[key] = &availabilityCheckResult{
		available: available,
		err:       err,
		timestamp: time.Now(),
	}
}

// Withdraw 从当前交易所提币到指定地址
func (n *ExchangeNode) Withdraw(amount float64, toAddress string, network string, memo string) (*model.WithdrawResponse, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	if toAddress == "" {
		return nil, fmt.Errorf("toAddress is required")
	}
	if err := n.ensureExchange(); err != nil {
		return nil, err
	}
	if network == "" {
		network = n.cfg.DefaultNetwork
	}

	// 对于统一账户，需要明确指定 walletType
	// 0 = 现货钱包（默认），1 = 资金钱包
	walletType := 0 // 默认使用现货钱包
	req := &model.WithdrawRequest{
		Asset:      n.cfg.Asset,
		Amount:     amount,
		Address:    toAddress,
		Network:    network,
		Memo:       memo,
		WalletType: &walletType, // 明确指定从现货钱包提币
	}
	return n.ex.Withdraw(req)
}

// CheckWithdrawStatus 基于提币记录检查提币是否完成（通过 WithdrawID 匹配）
func (n *ExchangeNode) CheckWithdrawStatus(withdrawID string) (bool, error) {
	if withdrawID == "" {
		return false, fmt.Errorf("withdrawID is empty")
	}
	if err := n.ensureExchange(); err != nil {
		return false, err
	}

	log := logger.GetLoggerInstance().Named("pipeline.exchange").Sugar()

	// 先按资产过滤查询；若未找到则不带资产再查一次（Bitget 等交易所 coin 格式可能不一致）
	tryAssets := []string{n.cfg.Asset}
	if n.cfg.Asset != "" {
		tryAssets = append(tryAssets, "")
	}

	for _, assetUsed := range tryAssets {
		records, err := n.ex.GetWithdrawHistory(assetUsed, 100)
		if err != nil {
			return false, fmt.Errorf("get withdraw history failed: %w", err)
		}

		log.Debugf("Checking withdraw status for ID: %s (asset=%q), found %d records", withdrawID, assetUsed, len(records))
		if len(records) > 0 && assetUsed != "" {
			// 调试：当按资产过滤有记录但未匹配时，打印前几条的 ID 便于排查 Bitget 等 ID 格式差异
			sampleIDs := make([]string, 0, 3)
			for i, r := range records {
				if i >= 3 {
					break
				}
				sampleIDs = append(sampleIDs, r.WithdrawID)
			}
			log.Debugf("Sample withdraw record IDs (asset=%q): %v", assetUsed, sampleIDs)
		}

		for _, r := range records {
			if r.WithdrawID != withdrawID {
				continue
			}
			log.Debugf("Found withdraw record: ID=%s, Status=%s, TxHash=%s, Asset=%s, Amount=%.8f, Network=%s",
				r.WithdrawID, r.Status, r.TxHash, r.Asset, r.Amount, r.Network)

			switch r.Status {
			case "COMPLETED":
				log.Infof("Withdraw %s completed successfully (txHash=%s)", withdrawID, r.TxHash)
				return true, nil
			case "REJECTED", "FAILED", "CANCELED":
				return false, fmt.Errorf("withdraw %s failed with status %s (txHash=%s)", withdrawID, r.Status, r.TxHash)
			default:
				// PENDING/PROCESSING 等：仅当 txHash 为真实链上哈希（0x+64hex）时才视为已广播可确认
				// Bitget 等交易所的 txId 可能返回 orderId 数字，非真实链上 hash，不能据此提前确认
				if isLikelyBlockchainTxHash(r.TxHash) {
					log.Infof("Withdraw %s has blockchain txHash=%s (status=%s), treating as confirmed", withdrawID, r.TxHash, r.Status)
					return true, nil
				}
				log.Debugf("Withdraw %s status: %s (waiting, txHash=%s)", withdrawID, r.Status, r.TxHash)
				return false, nil
			}
		}

		if assetUsed != "" {
			log.Debugf("Withdraw ID %s not found with asset=%s, retrying without asset filter", withdrawID, assetUsed)
		}
	}

	log.Debugf("Withdraw ID %s not found in history (may be API delay)", withdrawID)
	return false, nil
}

// isLikelyBlockchainTxHash 判断是否为真实链上交易哈希（0x+64hex），排除交易所 orderId 等数字 ID
func isLikelyBlockchainTxHash(s string) bool {
	if len(s) < 64 {
		return false
	}
	hexPart := s
	if len(s) >= 2 && (s[0] == '0' && (s[1] == 'x' || s[1] == 'X')) {
		hexPart = s[2:]
	}
	if len(hexPart) != 64 {
		return false
	}
	for _, c := range hexPart {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}

// --- 辅助函数 ---

// WaitForDepositConfirmed 轮询等待指定 txHash 的充币到账
// 该函数供执行引擎复用，不在 Node 接口中暴露。
func (n *ExchangeNode) WaitForDepositConfirmed(txHash string, maxWait time.Duration, interval time.Duration) (bool, error) {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		ok, err := n.CheckDepositStatus(txHash)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
		time.Sleep(interval)
	}
	return false, fmt.Errorf("deposit %s not confirmed within %s", txHash, maxWait.String())
}

