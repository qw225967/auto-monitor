package position

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/exchange"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/onchain"
	"github.com/qw225967/auto-monitor/internal/statistics"
	"github.com/qw225967/auto-monitor/internal/trader"
	"github.com/qw225967/auto-monitor/internal/utils/logger"
	"github.com/qw225967/auto-monitor/internal/utils/parallel"

	"go.uber.org/zap"
)

// 全局钱包管理器实例
var globalWalletManager *WalletManager
var globalWalletManagerOnce sync.Once

// OnchainClientConfig 链上客户端配置
// 一个链上客户端可以对应多个钱包地址
type OnchainClientConfig struct {
	Client              onchain.OnchainClient
	WalletAddresses     []string // 该客户端对应的所有钱包地址
	WalletAddressChains string
}

// WalletManager 钱包管理器
// 负责管理多个交易所和多个链上客户端的钱包信息的内存缓存和定时刷新
type WalletManager struct {
	mu sync.RWMutex

	// 钱包信息缓存
	walletInfo *model.WalletDetailInfo

	// 多个 Trader 实例（按交易所类型索引，如 "binance", "bybit" 等）
	traders map[string]trader.Trader

	// 多个链上客户端配置（按客户端ID索引）
	// 一个客户端可以对应多个钱包地址
	onchainClients map[string]*OnchainClientConfig

	// 刷新配置
	refreshInterval time.Duration // 刷新间隔
	lastRefreshTime time.Time     // 最后刷新时间

	// 上下文管理
	ctx    context.Context
	cancel context.CancelFunc

	// 协程组
	routineGroup *parallel.RoutineGroup

	// 日志实例
	logger *zap.SugaredLogger

	// 是否已初始化
	initialized bool
}

// InitWalletManager 初始化全局钱包管理器
// traders: Trader 列表（统一交易所和链上）
// onchainClients: 链上客户端配置列表（map[clientID]*OnchainClientConfig）
// refreshInterval: 刷新间隔
func InitWalletManager(traders []trader.Trader, onchainClients map[string]*OnchainClientConfig, refreshInterval time.Duration) error {
	var initErr error
	globalWalletManagerOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())

		globalWalletManager = &WalletManager{
			traders:         make(map[string]trader.Trader),
			onchainClients:  make(map[string]*OnchainClientConfig),
			refreshInterval: refreshInterval,
			walletInfo:      nil,
			ctx:             ctx,
			cancel:          cancel,
			routineGroup:    parallel.NewRoutineGroup(),
			logger:          logger.GetLoggerInstance().Named("WalletManager").Sugar(),
			initialized:     false,
		}

		// 添加 Trader
		for _, t := range traders {
			if t == nil {
				continue
			}
			traderType := t.GetType()
			if traderType == "" {
				globalWalletManager.logger.Warnf("Skipping trader with empty type")
				continue
			}
			// 提取交易所类型（去掉市场类型后缀，如 "binance:futures" -> "binance"）
			exchangeType := traderType
			if idx := strings.Index(traderType, ":"); idx > 0 {
				exchangeType = traderType[:idx]
			}
			globalWalletManager.traders[exchangeType] = t
			globalWalletManager.logger.Infof("Initialized trader: %s", traderType)
		}

		// 添加链上客户端
		for clientID, config := range onchainClients {
			if config == nil || config.Client == nil {
				globalWalletManager.logger.Warnf("Skipping onchain client %s: config is nil", clientID)
				continue
			}
			if len(config.WalletAddresses) == 0 {
				globalWalletManager.logger.Warnf("Skipping onchain client %s: no wallet addresses", clientID)
				continue
			}
			globalWalletManager.onchainClients[clientID] = config
			globalWalletManager.logger.Infof("Initialized onchain client: %s with %d wallet addresses", clientID, len(config.WalletAddresses))
		}

		globalWalletManager.initialized = true

		// 启动定时刷新任务
		globalWalletManager.routineGroup.GoSafe(func() {
			globalWalletManager.refreshLoop()
		})

		// 立即执行一次刷新
		if err := globalWalletManager.refresh(); err != nil {
			globalWalletManager.logger.Errorf("Failed to refresh wallet info on init: %v", err)
			initErr = err
		}

		globalWalletManager.logger.Infof("WalletManager initialized, refresh interval: %v, traders: %d, onchain clients: %d",
			refreshInterval, len(globalWalletManager.traders), len(globalWalletManager.onchainClients))
	})

	return initErr
}

// GetWalletManager 获取全局钱包管理器实例
func GetWalletManager() *WalletManager {
	return globalWalletManager
}

// GetOnchainClient 获取第一个可用的链上客户端（或根据 chainID 获取特定客户端）
func (wm *WalletManager) GetOnchainClient(chainID string) onchain.OnchainClient {
	if wm == nil {
		return nil
	}
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	// 如果指定了 chainID，尝试找到匹配的客户端
	if chainID != "" {
		for _, config := range wm.onchainClients {
			if config != nil && config.Client != nil {
				// 检查 chainID 是否在 WalletAddressChains 中
				if strings.Contains(config.WalletAddressChains, chainID) {
					return config.Client
				}
			}
		}
	}

	// 如果没有指定 chainID 或没找到匹配的，返回第一个可用的客户端
	for _, config := range wm.onchainClients {
		if config != nil && config.Client != nil {
			return config.Client
		}
	}

	return nil
}

// GetExchange 根据交易所类型获取底层 Exchange（用于 QuantoMultiplier 等）
// exchangeType 支持带市场后缀的格式（如 "binance:spot"、"binance:futures"），
// 会自动剥离后缀以匹配 WalletManager 注册时的键（如 "binance"）。
func (wm *WalletManager) GetExchange(exchangeType string) exchange.Exchange {
	if wm == nil {
		return nil
	}
	// 剥离市场类型后缀（如 "binance:spot" -> "binance"），与注册时保持一致
	if idx := strings.Index(exchangeType, ":"); idx > 0 {
		exchangeType = exchangeType[:idx]
	}
	wm.mu.RLock()
	t, ok := wm.traders[exchangeType]
	wm.mu.RUnlock()
	if !ok || t == nil {
		return nil
	}
	if c, ok := t.(*trader.CexTrader); ok {
		return c.GetExchange()
	}
	if d, ok := t.(*trader.DexTrader); ok {
		return d.GetExchange()
	}
	return nil
}

// UpdateTraders 更新 traders（用于配置更新后同步新的交易所实例）
func (wm *WalletManager) UpdateTraders(newTraders []trader.Trader) {
	if wm == nil {
		return
	}

	wm.mu.Lock()
	defer wm.mu.Unlock()

	// 清空旧的 traders
	wm.traders = make(map[string]trader.Trader)

	// 添加新的 traders
	for _, t := range newTraders {
		if t == nil {
			continue
		}
		traderType := t.GetType()
		if traderType == "" {
			continue
		}
		// 提取交易所类型（去掉市场类型后缀，如 "binance:futures" -> "binance"）
		exchangeType := traderType
		if idx := strings.Index(traderType, ":"); idx > 0 {
			exchangeType = traderType[:idx]
		}
		wm.traders[exchangeType] = t
		wm.logger.Infof("✅ 已更新 trader: %s", traderType)
	}

	wm.logger.Infof("✅ WalletManager traders 已更新，共 %d 个", len(wm.traders))
}

// Stop 停止钱包管理器
func (wm *WalletManager) Stop() {
	if wm == nil {
		return
	}
	wm.cancel()
	wm.routineGroup.Wait()
	wm.logger.Info("WalletManager stopped")
}

// StopWalletManager 停止全局钱包管理器
func StopWalletManager() {
	if globalWalletManager != nil {
		globalWalletManager.Stop()
	}
}

// UpdateOnchainClientConfig 动态更新链上客户端配置
// 当钱包配置更新时调用此方法来更新链上客户端
func UpdateOnchainClientConfig() error {
	walletManager := GetWalletManager()
	if walletManager == nil {
		return fmt.Errorf("WalletManager not initialized")
	}

	return walletManager.updateOnchainClientConfig()
}

// updateOnchainClientConfig 根据当前配置更新链上客户端
func (wm *WalletManager) updateOnchainClientConfig() error {
	if wm == nil {
		return fmt.Errorf("WalletManager is nil")
	}

	walletConfig := config.GetGlobalConfig().Wallet
	walletAddress := walletConfig.WalletAddress
	isWalletConfigured := walletAddress != "" && walletAddress != "请添加"

	wm.mu.Lock()
	defer wm.mu.Unlock()

	if isWalletConfigured {
		// 如果钱包已配置，检查是否已有链上客户端
		if _, exists := wm.onchainClients["default"]; !exists {
			// 创建新的链上客户端
			okOnChainClient := onchain.NewOkdex()
			if err := okOnChainClient.Init(); err != nil {
				wm.logger.Warnf("Failed to init onchain client: %v", err)
				return fmt.Errorf("failed to init onchain client: %w", err)
			}

			// 使用默认 BSC 链 (56)
			chainIndex := "56"

			wm.onchainClients["default"] = &OnchainClientConfig{
				Client:              okOnChainClient,
				WalletAddresses:     []string{walletAddress},
				WalletAddressChains: chainIndex,
			}
			wm.logger.Infof("✅ 链上客户端已动态添加，钱包地址: %s, 链ID: %s", walletAddress, chainIndex)
		} else {
			// 如果已存在，更新钱包地址和链ID
			chainIndex := "56"
			wm.onchainClients["default"].WalletAddresses = []string{walletAddress}
			wm.onchainClients["default"].WalletAddressChains = chainIndex
			wm.logger.Infof("✅ 链上客户端配置已更新，钱包地址: %s, 链ID: %s", walletAddress, chainIndex)
		}
	} else {
		// 如果钱包未配置，移除链上客户端
		if _, exists := wm.onchainClients["default"]; exists {
			delete(wm.onchainClients, "default")
			wm.logger.Info("ℹ️  钱包地址未配置，已移除链上客户端")
		}
	}

	return nil
}

// AddChains 动态扩展链上客户端的查询链列表（逗号分隔），用于 pipeline 配置了中间链时自动查询那些链的余额。
// 例如 pipeline BSC→ETH→Binance 中 ETH 是中间链，调用 AddChains("1") 后，
// 定时刷新会同时查询 chain 1 上的代币余额，前端即可显示中间链持仓。
func (wm *WalletManager) AddChains(chainIDs ...string) {
	if wm == nil || len(chainIDs) == 0 {
		return
	}
	wm.mu.Lock()
	defer wm.mu.Unlock()

	cfg, exists := wm.onchainClients["default"]
	if !exists || cfg == nil {
		return
	}

	existing := make(map[string]bool)
	for _, c := range strings.Split(cfg.WalletAddressChains, ",") {
		c = strings.TrimSpace(c)
		if c != "" {
			existing[c] = true
		}
	}

	changed := false
	for _, id := range chainIDs {
		id = strings.TrimSpace(id)
		if id != "" && !existing[id] {
			existing[id] = true
			changed = true
		}
	}

	if changed {
		parts := make([]string, 0, len(existing))
		for k := range existing {
			parts = append(parts, k)
		}
		cfg.WalletAddressChains = strings.Join(parts, ",")
		wm.logger.Infof("链上余额查询链列表已扩展: %s", cfg.WalletAddressChains)
	}
}

// AddChainsGlobal 便捷函数：向全局 WalletManager 扩展链列表
func AddChainsGlobal(chainIDs ...string) {
	wm := GetWalletManager()
	if wm != nil {
		wm.AddChains(chainIDs...)
	}
}

// refreshLoop 定时刷新循环
func (wm *WalletManager) refreshLoop() {
	ticker := time.NewTicker(wm.refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-wm.ctx.Done():
			return
		case <-ticker.C:
			parallel.RunSafe(func() {
				if err := wm.refresh(); err != nil {
					wm.logger.Errorf("Failed to refresh wallet info: %v", err)
				}
			})
		}
	}
}

// Refresh 刷新钱包信息
func (wm *WalletManager) refresh() error {
	wm.logger.Debug("Refreshing wallet info...")

	// 获取所有 Trader 和链上客户端配置
	wm.mu.RLock()
	traders := make(map[string]trader.Trader)
	for k, v := range wm.traders {
		traders[k] = v
	}
	onchainClients := make(map[string]*OnchainClientConfig)
	for k, v := range wm.onchainClients {
		onchainClients[k] = v
	}
	wm.mu.RUnlock()

	// 创建新的 WalletDetailInfo
	walletInfo := &model.WalletDetailInfo{
		ExchangeWallets: make(map[string]*model.ExchangeWalletInfo),
		OnchainBalances: make(map[string]map[string]model.OkexTokenAsset),
	}

	// 获取所有 Trader 的钱包信息
	for exchangeType, t := range traders {
		// 检查配置是否有效，如果无效则跳过该 Trader，继续处理其他 Trader
		if !wm.isExchangeConfigValid(exchangeType) {
			wm.logger.Debugf("Trader %s 配置无效（API key 未设置），跳过获取仓位", exchangeType)
			continue
		}

		exchangeWalletInfo, err := wm.getExchangeWalletInfo(exchangeType, t)
		if err != nil {
			wm.logger.Warnf("Failed to get wallet info for trader %s: %v", exchangeType, err)
			continue
		}
		walletInfo.ExchangeWallets[exchangeType] = exchangeWalletInfo
	}

	// 获取所有链上客户端的余额信息并合并到 OnchainBalances
	for clientID, config := range onchainClients {
		err := wm.mergeOnchainBalances(clientID, config, walletInfo)
		if err != nil {
			wm.logger.Warnf("Failed to get wallet info for onchain client %s: %v", clientID, err)
			continue
		}
	}

	// 聚合所有数据并更新统计信息
	wm.aggregateWalletInfo(walletInfo)

	// 更新缓存
	wm.mu.Lock()
	wm.walletInfo = walletInfo
	wm.lastRefreshTime = time.Now()
	wm.mu.Unlock()

	// 记录钱包数据到 StatisticsManager
	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager != nil {
		statisticsManager.RecordWallet(walletInfo)
	}

	wm.logger.Debugf("Wallet info refreshed, total asset: %.2f, exchanges: %d, positions: %d, onchain chains: %d",
		walletInfo.TotalAsset, walletInfo.ExchangeCount, walletInfo.PositionCount, walletInfo.OnchainChainCount)

	return nil
}

// getExchangeWalletInfo 获取单个 Trader 的钱包信息
func (wm *WalletManager) getExchangeWalletInfo(exchangeType string, t trader.Trader) (*model.ExchangeWalletInfo, error) {
	// 从 Trader 获取底层 Exchange（用于向后兼容）
	var ex exchange.Exchange
	if cexTrader, ok := t.(*trader.CexTrader); ok {
		ex = cexTrader.GetExchange()
	} else if dexTrader, ok := t.(*trader.DexTrader); ok {
		ex = dexTrader.GetExchange()
	}

	// 检查是否是 OnchainTrader
	if onchainTrader, ok := t.(trader.OnchainTrader); ok {
		// 链上 Trader，分别获取现货和合约余额
		spotBalances, err := onchainTrader.GetSpotBalances()
		if err != nil {
			spotBalances = make(map[string]*model.Balance)
		}

		futuresBalances, err := onchainTrader.GetFuturesBalances()
		if err != nil {
			futuresBalances = make(map[string]*model.Balance)
		}

		// 为了向后兼容，AccountBalances 聚合现货和合约余额
		accountBalances := make(map[string]*model.Balance)
		for asset, balance := range spotBalances {
			if balance != nil {
				accountBalances[asset] = balance
			}
		}
		for asset, balance := range futuresBalances {
			if balance != nil {
				if existing, exists := accountBalances[asset]; exists && existing != nil {
					if balance.Total > existing.Total {
						accountBalances[asset] = balance
					}
				} else {
					accountBalances[asset] = balance
				}
			}
		}

		positions := []*model.Position{} // 链上没有持仓概念

		// 转换为 ExchangeWalletInfo
		positionsMap := make(map[string]*model.Position)
		for _, pos := range positions {
			positionsMap[pos.Symbol] = pos
		}

		return &model.ExchangeWalletInfo{
			ExchangeType:       exchangeType,
			SpotBalances:       spotBalances,    // 现货余额
			FuturesBalances:    futuresBalances, // 合约余额
			AccountBalances:    accountBalances, // 向后兼容字段（聚合后的余额）
			Positions:          positionsMap,
			TotalBalanceValue:  0, // 需要计算
			TotalPositionValue: 0, // 需要计算
			TotalUnrealizedPnl: 0, // 需要计算
			PositionCount:      len(positions),
		}, nil
	}

	// 使用内部辅助函数获取单个交易所的信息
	// 注意：getSingleExchangeWalletInfo 已经调用了 UpdateStatistics 计算统计信息
	singleWalletInfo, err := getSingleExchangeWalletInfo(ex)
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet detail info: %w", err)
	}

	// 转换为 ExchangeWalletInfo（直接使用已计算的统计值）
	// 从 ExchangeWallets 中获取数据
	var accountBalances map[string]*model.Balance
	var spotBalances map[string]*model.Balance
	var futuresBalances map[string]*model.Balance
	var positions map[string]*model.Position
	var positionCount int

	if singleWalletInfo.ExchangeWallets != nil {
		if exchangeWallet, exists := singleWalletInfo.ExchangeWallets[exchangeType]; exists && exchangeWallet != nil {
			spotBalances = exchangeWallet.SpotBalances
			futuresBalances = exchangeWallet.FuturesBalances
			accountBalances = exchangeWallet.AccountBalances
			positions = exchangeWallet.Positions
			positionCount = exchangeWallet.PositionCount
		}
	}

	// 注意：如果 spotBalances 或 futuresBalances 为 nil，使用 accountBalances 作为后备
	if spotBalances == nil {
		spotBalances = accountBalances
	}
	if futuresBalances == nil {
		futuresBalances = accountBalances
	}
	// 如果 accountBalances 也为 nil，从 spotBalances 和 futuresBalances 聚合
	if accountBalances == nil {
		accountBalances = make(map[string]*model.Balance)
		for asset, balance := range spotBalances {
			if balance != nil {
				accountBalances[asset] = balance
			}
		}
		for asset, balance := range futuresBalances {
			if balance != nil {
				if existing, exists := accountBalances[asset]; exists && existing != nil {
					if balance.Total > existing.Total {
						accountBalances[asset] = balance
					}
				} else {
					accountBalances[asset] = balance
				}
			}
		}
	}

	exchangeWalletInfo := &model.ExchangeWalletInfo{
		ExchangeType:       exchangeType,
		SpotBalances:       spotBalances,    // 现货余额
		FuturesBalances:    futuresBalances, // 合约余额
		AccountBalances:    accountBalances, // 向后兼容字段
		Positions:          positions,
		TotalBalanceValue:  singleWalletInfo.TotalBalanceValue,
		TotalPositionValue: singleWalletInfo.TotalPositionValue,
		TotalUnrealizedPnl: singleWalletInfo.TotalUnrealizedPnl,
		PositionCount:      positionCount,
	}

	return exchangeWalletInfo, nil
}

// isExchangeConfigValid 检查交易所配置是否有效
// 如果 API key 未设置（为空或为 "请添加"），返回 false
// 同时检查 API key 格式是否有效（Binance API key 通常以字母开头，长度大于 10）
func (wm *WalletManager) isExchangeConfigValid(exchangeType string) bool {
	globalConfig := config.GetGlobalConfig()
	if globalConfig == nil {
		return false
	}

	switch exchangeType {
	case constants.ConnectTypeBinance:
		// 检查 Binance 配置
		apiKey := globalConfig.Binance.APIKey
		secretKey := globalConfig.Binance.SecretKey
		if apiKey == "" || secretKey == "" || apiKey == "请添加" || secretKey == "请添加" {
			return false
		}
		// 检查 API key 格式是否有效（Binance API key 通常长度大于 10，且不是占位符）
		if len(apiKey) < 10 || len(secretKey) < 10 {
			wm.logger.Debugf("Exchange %s API key 格式无效（长度过短）", exchangeType)
			return false
		}
		return true
	case constants.ConnectTypeByBit:
		// 检查 Bybit 配置
		apiKey := globalConfig.Bybit.APIKey
		secret := globalConfig.Bybit.Secret
		if apiKey == "" || secret == "" || apiKey == "请添加" || secret == "请添加" {
			return false
		}
		return true
	case constants.ConnectTypeBitGet:
		// 检查 BitGet 配置
		apiKey := globalConfig.BitGet.APIKey
		secret := globalConfig.BitGet.Secret
		passphrase := globalConfig.BitGet.Passphrase
		if apiKey == "" || secret == "" || passphrase == "" ||
			apiKey == "请添加" || secret == "请添加" || passphrase == "请添加" {
			return false
		}
		return true
	case constants.ConnectTypeGate:
		// 检查 Gate.io 配置
		apiKey := globalConfig.Gate.APIKey
		secret := globalConfig.Gate.Secret
		if apiKey == "" || secret == "" || apiKey == "请添加" || secret == "请添加" {
			return false
		}
		return true
	case constants.ConnectTypeOKEX:
		// 优先检查 OKX 配置段；若为空则回退到 OkEx.KeyList
		if globalConfig.OKX.APIKey != "" && globalConfig.OKX.Secret != "" && globalConfig.OKX.Passphrase != "" &&
			globalConfig.OKX.APIKey != "请添加" && globalConfig.OKX.Secret != "请添加" && globalConfig.OKX.Passphrase != "请添加" {
			return true
		}
		if len(globalConfig.OkEx.KeyList) > 0 {
			k := globalConfig.OkEx.KeyList[0]
			if k.APIKey != "" && k.Secret != "" && k.Passphrase != "" &&
				k.APIKey != "请添加" && k.Secret != "请添加" && k.Passphrase != "请添加" {
				return true
			}
		}
		return false
	case constants.ConnectTypeHyperliquid:
		// 检查 Hyperliquid 配置（需要钱包地址和 API 私钥）
		userAddress := globalConfig.Hyperliquid.UserAddress
		apiPrivateKey := globalConfig.Hyperliquid.APIPrivateKey
		if userAddress == "" || apiPrivateKey == "" ||
			userAddress == "请添加" || apiPrivateKey == "请添加" {
			return false
		}
		return true
	default:
		// 对于其他交易所，默认返回 true（允许尝试）
		return true
	}
}

// isReasonablePriceForSymbol 判断价格是否在合理区间，用于过滤同名假币（如 scam BNB）
func isReasonablePriceForSymbol(symbol string, price float64) bool {
	switch strings.ToUpper(symbol) {
	case "BNB", "WBNB":
		return price >= 400 && price <= 1200
	case "ETH", "WETH":
		return price >= 2000 && price <= 6000
	case "USDT", "USDC", "BUSD":
		return price >= 0.99 && price <= 1.01
	case "MATIC", "WMATIC", "POL":
		return price >= 0.3 && price <= 3
	case "AVAX", "WAVAX":
		return price >= 20 && price <= 80
	case "FTM", "WFTM":
		return price >= 0.3 && price <= 3
	default:
		return price > 0 && price < 1e9 // 排除明显异常
	}
}

// assetValue 计算资产 USDT 价值
func assetValue(a *model.OkexTokenAsset) float64 {
	bal, _ := strconv.ParseFloat(a.Balance, 64)
	if bal <= 0 {
		return 0
	}
	if a.Symbol == "USDT" || a.Symbol == "USDC" || a.Symbol == "BUSD" {
		return bal
	}
	price, _ := strconv.ParseFloat(a.TokenPrice, 64)
	return bal * price
}

// mergeOnchainBalances 获取单个链上客户端的余额并合并到 walletInfo.OnchainBalances
// 会合并该客户端所有钱包地址的余额
func (wm *WalletManager) mergeOnchainBalances(clientID string, config *OnchainClientConfig, walletInfo *model.WalletDetailInfo) error {
	if config == nil || config.Client == nil {
		return fmt.Errorf("onchain client config is nil")
	}

	// 合并所有钱包地址的余额（去重地址，避免同一地址重复查询导致余额翻倍）
	seenAddrs := make(map[string]bool)
	chainMap := make(map[string]map[string]*model.OkexTokenAsset) // chainIndex -> Symbol -> asset
	for _, walletAddress := range config.WalletAddresses {
		addr := strings.TrimSpace(strings.ToLower(walletAddress))
		if addr == "" {
			continue
		}
		if seenAddrs[addr] {
			continue
		}
		seenAddrs[addr] = true

		// 获取该钱包地址的链上余额
		excludeRiskToken := true
		assets, err := config.Client.GetAllTokenBalances(walletAddress, config.WalletAddressChains, excludeRiskToken)
		if err != nil {
			wm.logger.Warnf("Failed to get onchain balances for wallet %s: %v", walletAddress, err)
			continue
		}

		// 按链分组，使用 chain+symbol+TokenContractAddress 作为 key，避免同名假币（如 scam BNB）与真币合并
		for _, asset := range assets {
			if asset.ChainIndex == "" || asset.Symbol == "" {
				continue
			}
			if chainMap[asset.ChainIndex] == nil {
				chainMap[asset.ChainIndex] = make(map[string]*model.OkexTokenAsset)
			}
			// 同一 symbol 不同合约地址 = 不同代币（如真 BNB vs 假 BNB），不合并
			contractKey := strings.TrimSpace(strings.ToLower(asset.TokenContractAddress))
			if contractKey == "" || contractKey == "0x" || contractKey == "0x0000000000000000000000000000000000000000" {
				contractKey = "native"
			}
			key := asset.Symbol + ":" + contractKey

			existingAsset, exists := chainMap[asset.ChainIndex][key]
			if exists {
				// 同一合约地址的余额才合并（多钱包场景）
				existingBalance, _ := strconv.ParseFloat(existingAsset.Balance, 64)
				newBalance, _ := strconv.ParseFloat(asset.Balance, 64)
				existingAsset.Balance = strconv.FormatFloat(existingBalance+newBalance, 'f', -1, 64)
			} else {
				newAsset := asset
				chainMap[asset.ChainIndex][key] = &newAsset
			}
		}
	}

	// 合并到 walletInfo.OnchainBalances：同一 symbol 可能有多条（不同合约），需按价格合理性选取
	for chainIndex, tokenMap := range chainMap {
		if walletInfo.OnchainBalances[chainIndex] == nil {
			walletInfo.OnchainBalances[chainIndex] = make(map[string]model.OkexTokenAsset)
		}
		// 按 symbol 分组，同 symbol 多条时只保留价格合理的（过滤 scam 假币）
		symbolToBest := make(map[string]*model.OkexTokenAsset)
		for _, asset := range tokenMap {
			symbol := asset.Symbol
			price, _ := strconv.ParseFloat(asset.TokenPrice, 64)
			if !isReasonablePriceForSymbol(symbol, price) {
				continue
			}
			existing, ok := symbolToBest[symbol]
			if !ok || assetValue(asset) > assetValue(existing) {
				symbolToBest[symbol] = asset
			}
		}
		for symbol, asset := range symbolToBest {
			existingAsset, exists := walletInfo.OnchainBalances[chainIndex][symbol]
			if exists {
				existingBalance, _ := strconv.ParseFloat(existingAsset.Balance, 64)
				newBalance, _ := strconv.ParseFloat(asset.Balance, 64)
				existingAsset.Balance = strconv.FormatFloat(existingBalance+newBalance, 'f', -1, 64)
				walletInfo.OnchainBalances[chainIndex][symbol] = existingAsset
			} else {
				walletInfo.OnchainBalances[chainIndex][symbol] = *asset
			}
		}
	}

	return nil
}

// aggregateWalletInfo 聚合所有交易所和链上客户端的数据，更新总统计信息
func (wm *WalletManager) aggregateWalletInfo(walletInfo *model.WalletDetailInfo) {
	aggregateWalletInfo(walletInfo)
}

// GetWalletInfo 获取钱包信息（从缓存）
func (wm *WalletManager) GetWalletInfo() *model.WalletDetailInfo {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	if wm.walletInfo == nil {
		return nil
	}

	// 返回副本，避免外部修改
	return copyWalletInfo(wm.walletInfo)
}

// GetLastRefreshTime 获取最后刷新时间
func (wm *WalletManager) GetLastRefreshTime() time.Time {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.lastRefreshTime
}

// ForceRefresh 强制刷新钱包余额（交易成功后调用，确保余额数据是最新的）
func (wm *WalletManager) ForceRefresh() error {
	wm.logger.Info("强制刷新钱包余额...")
	return wm.refresh()
}

// copyWalletInfo 复制钱包信息（深拷贝）
func copyWalletInfo(src *model.WalletDetailInfo) *model.WalletDetailInfo {
	if src == nil {
		return nil
	}

	// 复制交易所钱包信息
	exchangeWallets := make(map[string]*model.ExchangeWalletInfo)
	if src.ExchangeWallets != nil {
		for exchangeType, exchangeWallet := range src.ExchangeWallets {
			if exchangeWallet == nil {
				continue
			}
			// 复制余额
			balancesMap := make(map[string]*model.Balance)
			if exchangeWallet.AccountBalances != nil {
				for asset, balance := range exchangeWallet.AccountBalances {
					if balance != nil {
						balancesMap[asset] = &model.Balance{
							Asset:      balance.Asset,
							Available:  balance.Available,
							Locked:     balance.Locked,
							Total:      balance.Total,
							UpdateTime: balance.UpdateTime,
						}
					}
				}
			}
			// 复制持仓
			positionsMap := make(map[string]*model.Position)
			if exchangeWallet.Positions != nil {
				for symbol, pos := range exchangeWallet.Positions {
					if pos != nil {
						positionsMap[symbol] = &model.Position{
							Symbol:        pos.Symbol,
							Side:          pos.Side,
							Size:          pos.Size,
							EntryPrice:    pos.EntryPrice,
							MarkPrice:     pos.MarkPrice,
							UnrealizedPnl: pos.UnrealizedPnl,
							Leverage:      pos.Leverage,
							UpdateTime:    pos.UpdateTime,
						}
					}
				}
			}
			// 注意：目前交易所 API 返回的是统一余额，暂时同时设置到 SpotBalances 和 FuturesBalances
			// 后续如果交易所 API 支持分别获取现货和合约余额，可以分别设置
			exchangeWallets[exchangeType] = &model.ExchangeWalletInfo{
				ExchangeType:       exchangeWallet.ExchangeType,
				SpotBalances:       balancesMap, // 现货余额（目前与统一余额相同）
				FuturesBalances:    balancesMap, // 合约余额（目前与统一余额相同）
				AccountBalances:    balancesMap, // 向后兼容字段
				Positions:          positionsMap,
				TotalBalanceValue:  exchangeWallet.TotalBalanceValue,
				TotalPositionValue: exchangeWallet.TotalPositionValue,
				TotalUnrealizedPnl: exchangeWallet.TotalUnrealizedPnl,
				PositionCount:      exchangeWallet.PositionCount,
			}
		}
	}

	// 复制链上余额信息
	onchainBalances := make(map[string]map[string]model.OkexTokenAsset)
	if src.OnchainBalances != nil {
		for chainIndex, symbolMap := range src.OnchainBalances {
			if symbolMap != nil {
				copiedMap := make(map[string]model.OkexTokenAsset)
				for symbol, asset := range symbolMap {
					copiedMap[symbol] = asset
				}
				onchainBalances[chainIndex] = copiedMap
			}
		}
	}

	return &model.WalletDetailInfo{
		ExchangeWallets:    exchangeWallets,
		OnchainBalances:    onchainBalances,
		TotalAsset:         src.TotalAsset,
		TotalUnrealizedPnl: src.TotalUnrealizedPnl,
		TotalPositionValue: src.TotalPositionValue,
		TotalBalanceValue:  src.TotalBalanceValue,
		TotalOnchainValue:  src.TotalOnchainValue,
		PositionCount:      src.PositionCount,
		OnchainChainCount:  src.OnchainChainCount,
		ExchangeCount:      src.ExchangeCount,
		UpdateTime:         src.UpdateTime,
	}
}
