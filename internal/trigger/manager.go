package trigger

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/exchange"
	// "auto-arbitrage/internal/exchange/aster" // 暂不初始化，避免报错
	"auto-arbitrage/internal/exchange/binance"
	"auto-arbitrage/internal/exchange/bitget"
	"auto-arbitrage/internal/exchange/bybit"
	"auto-arbitrage/internal/exchange/gate"
	"auto-arbitrage/internal/exchange/hyperliquid"
	// "auto-arbitrage/internal/exchange/lighter" // 暂不初始化，避免报错
	"auto-arbitrage/internal/exchange/okx"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/proto"
	"auto-arbitrage/internal/trader"
	"auto-arbitrage/internal/utils/common/snowflake"
	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/utils/parallel"
)

type TriggerManager struct {
	idGen snowflake.IdGen // ID 生成器

	triggers sync.Map // 触发器映射 key: trigger ID value: *Trigger

	subExchangePriceEventBus *SubPriceEventBus //订阅交易所价格事件总线

	traders map[constants.ExchangeType]trader.Trader //Trader 实例（统一交易所和链上）

	exchangePriceMsgChan chan ExchangePriceMsg // 从exchange订阅价格的消息队列

	// 上下文管理
	context context.Context
	cancel  context.CancelFunc

	routineGroup *parallel.RoutineGroup

	// 管理 trigger 内部的协程上下文
	triggerContext       context.Context
	triggerContextCancel context.CancelFunc

	// 管理 trigger 内部的协程组
	triggerRoutineGroup *parallel.RoutineGroup

	// 套保价订阅与缓存：key = symbol:triggerIDStr:exchangeType，删除 trigger 时按 triggerID 清理
	hedgingPriceSubs      map[string]struct{}       // 存在即表示该 key 需要套保合约价
	lastHedgingTicker     map[string]*model.Ticker  // 最新 ticker
	triggerIDToHedgingKeys map[uint64][]string      // triggerId -> keys，便于 removeTrigger 时清理
	hedgingMu             sync.RWMutex

	// 日志实例
	logger *zap.SugaredLogger
}

// ExchangePriceMsg 从交易所订阅的价格消息
type ExchangePriceMsg struct {
	symbol       string
	ticker       *model.Ticker
	exchangeType constants.ExchangeType // 用于 handlePriceMsgChan 按交易所路由到对应 trigger 的 source
	marketType   string                 // 市场类型（"spot" 或 "futures"），用于区分同一交易所的现货和合约价格
}

// OnChainPriceMsg 从链上订阅的价格消息
type OnChainPriceMsg struct {
	price *model.ChainPriceInfo
}

// SubPriceEventBus trigger 订阅价格事件总线
// key: symbol+marketType (如 "BTCUSDT:spot" 或 "BTCUSDT:futures")
type SubPriceEventBus struct {
	subEventBus map[string][]uint64
	mu          sync.RWMutex
}

// buildKey 构建订阅 key：symbol+marketType
func buildKey(symbol, marketType string) string {
	if marketType == "" {
		marketType = "futures" // 默认使用 futures
	}
	return symbol + ":" + marketType
}

// buildHedgingKey 构建套保价缓存 key：symbol:triggerIDStr:exchangeType
func buildHedgingKey(triggerSymbol, triggerIDStr, exchangeType string) string {
	return normalizeSymbolForExchange(triggerSymbol) + ":" + triggerIDStr + ":" + strings.ToLower(strings.TrimSpace(exchangeType))
}

// subscribe 订阅价格事件
func (s *SubPriceEventBus) subscribe(symbol string, marketType string, triggerId uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := buildKey(symbol, marketType)
	if s.subEventBus[key] == nil {
		s.subEventBus[key] = make([]uint64, 0)
	}

	s.subEventBus[key] = append(s.subEventBus[key], triggerId)
}

// unsubscribe 取消订阅价格事件
func (s *SubPriceEventBus) unsubscribe(symbol string, marketType string, triggerId uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := buildKey(symbol, marketType)
	ids, ok := s.subEventBus[key]
	if !ok {
		return
	}

	for i, id := range ids {
		if id == triggerId {
			// 删除切片元素：将最后一个元素移到当前位置，然后切掉最后一个
			lastIdx := len(ids) - 1
			ids[i] = ids[lastIdx]
			s.subEventBus[key] = ids[:lastIdx]

			if len(s.subEventBus[key]) == 0 {
				delete(s.subEventBus, key)
			}
			break
		}
	}
}

// NewTriggerManager 创建管理器
func NewTriggerManager() *TriggerManager {
	tm := &TriggerManager{
		idGen: snowflake.NewIdGen(),

		triggers: sync.Map{},

		subExchangePriceEventBus: &SubPriceEventBus{
			subEventBus: make(map[string][]uint64),
			mu:          sync.RWMutex{},
		},

		hedgingPriceSubs:       make(map[string]struct{}),
		lastHedgingTicker:      make(map[string]*model.Ticker),
		triggerIDToHedgingKeys: make(map[uint64][]string),

		exchangePriceMsgChan: make(chan ExchangePriceMsg, 2048),

		routineGroup:        parallel.NewRoutineGroup(),
		triggerRoutineGroup: parallel.NewRoutineGroup(),

		logger: logger.GetLoggerInstance().Named("TriggerManager").Sugar(),
	}

	tm.context, tm.cancel = context.WithCancel(context.Background())
	tm.triggerContext, tm.triggerContextCancel = context.WithCancel(context.Background())

	tm.init()

	return tm
}

// initTraders 初始化 Trader 实例（统一交易所和链上）
// 每个交易所设置独立的 TickerCallback，在消息中带上 exchangeType，由 handlePriceMsgChan 按交易所路由；
// 不再把 SetTickerCallback 交给 CexTrader/DexTrader，避免被 SetPriceCallback 覆盖导致仅能存一个回调。
func initTraders(tm *TriggerManager) map[constants.ExchangeType]trader.Trader {
	traders := make(map[constants.ExchangeType]trader.Trader)

	setCallback := func(ex exchange.Exchange, et constants.ExchangeType) {
		ex.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
			tm.exchangePriceMsgChan <- ExchangePriceMsg{
				symbol:       symbol,
				ticker:       ticker,
				exchangeType: et,
				marketType:   marketType,
			}
		})
	}

	// 初始化 Binance (CEX)
	binanceEx := binance.NewBinance(config.GetGlobalConfig().Binance.APIKey, config.GetGlobalConfig().Binance.SecretKey)
	if err := binanceEx.Init(); err == nil {
		setCallback(binanceEx, constants.ExchangeBinance)
		traders[constants.ExchangeBinance] = trader.NewCexTrader(binanceEx)
	}

	// 初始化 Gate.io (CEX) - API 密钥从全局配置获取
	gateEx := gate.NewGate()
	if err := gateEx.Init(); err == nil {
		setCallback(gateEx, constants.ExchangeGate)
		traders[constants.ExchangeGate] = trader.NewCexTrader(gateEx)
	}

	// 初始化 Bybit (CEX) - API 密钥从全局配置获取
	bybitEx := bybit.NewBybit()
	if err := bybitEx.Init(); err == nil {
		setCallback(bybitEx, constants.ExchangeByBit)
		traders[constants.ExchangeByBit] = trader.NewCexTrader(bybitEx)
	}

	// 初始化 Bitget (CEX) - API 密钥从全局配置获取
	bitgetEx := bitget.NewBitget()
	if err := bitgetEx.Init(); err == nil {
		setCallback(bitgetEx, constants.ExchangeBitGet)
		traders[constants.ExchangeBitGet] = trader.NewCexTrader(bitgetEx)
	}

	// 初始化 OKX (CEX) - API 密钥从全局配置 OkEx.KeyList 获取
	okxEx := okx.NewOkx()
	if err := okxEx.Init(); err == nil {
		setCallback(okxEx, constants.ExchangeOKEX)
		traders[constants.ExchangeOKEX] = trader.NewCexTrader(okxEx)
	}

	// DEX 集成
	// 初始化 Aster (DEX) — 暂注释，避免报错
	// asterEx := aster.NewAster(
	// 	config.GetGlobalConfig().Aster.APIKey,
	// 	config.GetGlobalConfig().Aster.Secret,
	// )
	// if err := asterEx.Init(); err == nil {
	// 	setCallback(asterEx, constants.ExchangeAster)
	// 	traders[constants.ExchangeAster] = trader.NewDexTrader(asterEx)
	// }

	// 初始化 Hyperliquid (DEX)
	hyperliquidEx := hyperliquid.NewHyperliquid(
		config.GetGlobalConfig().Hyperliquid.UserAddress,
		config.GetGlobalConfig().Hyperliquid.APIPrivateKey,
	)
	if err := hyperliquidEx.Init(); err == nil {
		setCallback(hyperliquidEx, constants.ExchangeHyperliquid)
		traders[constants.ExchangeHyperliquid] = trader.NewDexTrader(hyperliquidEx)
	}

	// 初始化 Lighter (DEX) — 暂注释，避免报错
	// lighterEx := lighter.NewLighter(
	// 	config.GetGlobalConfig().Lighter.APIKey,
	// 	config.GetGlobalConfig().Lighter.Secret,
	// )
	// if err := lighterEx.Init(); err == nil {
	// 	setCallback(lighterEx, constants.ExchangeLighter)
	// 	traders[constants.ExchangeLighter] = trader.NewDexTrader(lighterEx)
	// }

	return traders
}

// ReinitTraders 重新初始化所有 traders（用于配置更新后）
// 重新创建交易所实例并使用最新的配置
// 注意：重新初始化后，已存在的 trigger 需要重新订阅才能使用新的配置
func (tm *TriggerManager) ReinitTraders() error {
	// 重新初始化所有 traders
	newTraders := initTraders(tm)

	// 更新 traders map
	tm.traders = newTraders

	tm.logger.Infof("已重新初始化所有 traders，共 %d 个", len(newTraders))
	tm.logger.Warnf("注意：已存在的 trigger 需要重新订阅才能使用新的 API 密钥配置")

	return nil
}

// GetAllTraders 获取所有 traders（用于同步到 WalletManager）
func (tm *TriggerManager) GetAllTraders() []trader.Trader {
	tradersList := make([]trader.Trader, 0, len(tm.traders))
	for _, t := range tm.traders {
		if t != nil {
			tradersList = append(tradersList, t)
		}
	}
	return tradersList
}

// GetExchangeSource 实现 proto.TriggerManager 接口
// 返回 Trader 接口（向后兼容）
func (tm *TriggerManager) GetExchangeSource(exchangeType interface{}) interface{} {
	if et, ok := exchangeType.(constants.ExchangeType); ok {
		return tm.getExchangeSourceInternal(et)
	}
	return nil
}

// getExchangeSourceInternal 内部方法，使用具体类型
// 返回 Trader 接口（向后兼容，可以通过类型断言获取底层 Exchange）
func (tm *TriggerManager) getExchangeSourceInternal(exchangeType constants.ExchangeType) trader.Trader {
	if t, ok := tm.traders[exchangeType]; ok {
		return t
	}
	return nil
}

// init 初始化管理器
func (tm *TriggerManager) init() {
	tm.traders = initTraders(tm)
}

// AddTrigger 实现 proto.TriggerManager 接口
func (tm *TriggerManager) AddTrigger(symbol string, trigger proto.Trigger) error {
	// 支持普通 Trigger
	if t, ok := trigger.(*Trigger); ok {
		return tm.addTriggerInternal(symbol, t)
	}
	// 支持 BrickMovingTrigger
	if bmt, ok := trigger.(*BrickMovingTrigger); ok {
		return tm.addBrickMovingTriggerInternal(symbol, bmt)
	}
	return fmt.Errorf("invalid trigger type: %T", trigger)
}

// subscribeExchangeTicker 辅助函数：订阅交易所 ticker（已废弃，使用 Trader.Subscribe 替代）
// 保留此函数用于向后兼容
func (tm *TriggerManager) subscribeExchangeTicker(exch exchange.Exchange, symbol string) error {
	exchangeTypeStr := exch.GetType()
	var exchangeType constants.ExchangeType
	switch exchangeTypeStr {
	case "binance":
		exchangeType = constants.ExchangeBinance
	case "gate":
		exchangeType = constants.ExchangeGate
	case "bybit":
		exchangeType = constants.ExchangeByBit
	case "bitget":
		exchangeType = constants.ExchangeBitGet
	case "okex":
		exchangeType = constants.ExchangeOKEX
	case "aster":
		exchangeType = constants.ExchangeAster
	case "hyperliquid":
		exchangeType = constants.ExchangeHyperliquid
	default:
		exchangeType = constants.ExchangeBinance
		tm.logger.Warnf("Unknown exchange type: %s, using Binance as default", exchangeTypeStr)
	}

	// 订阅对应的交易所 ticker
	// 注意：subscribeExchangeTicker 不直接知道 marketType，应该从调用方传递
	// 这里默认使用 "futures"，但建议调用方在 addTriggerInternal 中直接调用 Subscribe
	if targetTrader, ok := tm.traders[exchangeType]; ok && targetTrader != nil {
		if err := targetTrader.Subscribe(symbol, "futures"); err != nil {
			return fmt.Errorf("failed to subscribe %s ticker: %w", exchangeTypeStr, err)
		}
		tm.logger.Debugf("Subscribed to %s ticker for symbol: %s", exchangeTypeStr, symbol)
		return nil
	}
	return fmt.Errorf("trader %s not found in manager", exchangeTypeStr)
}

// isOnchainType 判断 traderType 是否是链上类型
func isOnchainType(traderType string) bool {
	return traderType != "" && strings.HasPrefix(traderType, "onchain:")
}

// normalizeSymbolForExchange 若 symbol 不以 USDT/USDC/BUSD 结尾则补上 USDT，便于 CEX 订阅与 event bus 路由。
// Token 映射的 symbol 多为纯 base（如 RAVE），CEX 需要 RAVEUSDT；链上 subscribeOnchainForSource 内部用 TrimSuffix 取 base，仍传原始 symbol 即可。
func normalizeSymbolForExchange(symbol string) string {
	s := strings.ToUpper(symbol)
	if strings.HasSuffix(s, "USDT") || strings.HasSuffix(s, "USDC") || strings.HasSuffix(s, "BUSD") {
		return symbol
	}
	return symbol + "USDT"
}

// parseTraderTypeFromString 解析 traderType 字符串
// 格式: "type:value" (如 "binance:futures" 或 "onchain:56")
// 返回: traderTypeStr, chainId, marketType, error
func parseTraderTypeFromString(traderType string) (traderTypeStr, chainId, marketType string, err error) {
	if traderType == "" {
		return "", "", "", fmt.Errorf("trader type is empty")
	}

	parts := strings.Split(traderType, ":")
	if len(parts) != 2 {
		return "", "", "", fmt.Errorf("invalid trader type format: %s", traderType)
	}

	traderTypeStr = parts[0]
	value := parts[1]

	if traderTypeStr == "onchain" {
		chainId = value
		return traderTypeStr, chainId, "", nil
	}

	// 交易所类型（如 binance, gate, bybit, bitget 等）
	marketType = value // value 是 marketType（spot 或 futures）
	return traderTypeStr, "", marketType, nil
}

// addTriggerInternal 内部方法，接受具体类型
func (tm *TriggerManager) addTriggerInternal(symbol string, trigger *Trigger) error {
	tm.triggers.Store(trigger.ID, trigger)
	// event bus 与 CEX 使用带计价币的 symbol（如 RAVEUSDT），以匹配交易所 ticker；token 映射常只有 base（RAVE）
	exchangeSymbol := normalizeSymbolForExchange(symbol)
	
	// 订阅 sourceA 的价格事件（如果是交易所类型）
	if !isOnchainType(trigger.traderAType) && trigger.sourceA != nil {
		_, _, aMarketType, _ := parseTraderTypeFromString(trigger.traderAType)
		if aMarketType == "" {
			aMarketType = "futures" // 默认使用 futures
		}
		tm.subExchangePriceEventBus.subscribe(exchangeSymbol, aMarketType, trigger.ID)
	}
	
	// 订阅 sourceB 的价格事件（如果是交易所类型）
	if !isOnchainType(trigger.traderBType) && trigger.sourceB != nil {
		_, _, bMarketType, _ := parseTraderTypeFromString(trigger.traderBType)
		if bMarketType == "" {
			bMarketType = "futures" // 默认使用 futures
		}
		tm.subExchangePriceEventBus.subscribe(exchangeSymbol, bMarketType, trigger.ID)
	}

	// 处理 sourceA 的订阅
	// 优先根据类型判断，而不是根据 sourceA 是否为 nil
	if isOnchainType(trigger.traderAType) {
		// sourceA 是链上类型，需要订阅链上（传原始 symbol，内部 TrimSuffix 取 base 做 token 映射）
		if trigger.sourceA != nil {
			tm.logger.Debugf("SourceA is already set as onchain trader, skipping subscription")
		} else {
			if err := trigger.subscribeOnchainForSource(symbol, true); err != nil {
				tm.removeTrigger(trigger.ID)
				return fmt.Errorf("failed to subscribe sourceA onchain: %w", err)
			}
			tm.logger.Debugf("Subscribed sourceA onchain for symbol: %s", symbol)
		}
	} else if trigger.sourceA != nil {
		_, _, aMarketType, _ := parseTraderTypeFromString(trigger.traderAType)
		
		// 添加重试机制，处理网络错误（如 broken pipe）
		maxRetries := 3
		var lastErr error
		for attempt := 1; attempt <= maxRetries; attempt++ {
			err := trigger.sourceA.Subscribe(exchangeSymbol, aMarketType)
			if err == nil {
				tm.logger.Debugf("Subscribed sourceA trader for symbol: %s (marketType=%s)", exchangeSymbol, aMarketType)
				break
			}
			
			lastErr = err
			errStr := err.Error()
			// 检查是否是网络错误（broken pipe, connection closed 等）
			isNetworkError := strings.Contains(errStr, "broken pipe") ||
				strings.Contains(errStr, "connection closed") ||
				strings.Contains(errStr, "write tcp") ||
				strings.Contains(errStr, "EOF")
			
			if isNetworkError && attempt < maxRetries {
				// 网络错误，等待后重试
				waitTime := time.Duration(attempt) * time.Second
				tm.logger.Warnf("订阅 sourceA trader 失败（尝试 %d/%d）: %v，%v 后重试...", 
					attempt, maxRetries, err, waitTime)
				time.Sleep(waitTime)
				continue
			}
			
			// 非网络错误或已达到最大重试次数
			tm.removeTrigger(trigger.ID)
			return fmt.Errorf("failed to subscribe sourceA trader after %d attempts: %w", attempt, err)
		}
		
		if lastErr != nil {
			tm.removeTrigger(trigger.ID)
			return fmt.Errorf("failed to subscribe sourceA trader: %w", lastErr)
		}
	}

	// 处理 sourceB 的订阅
	if isOnchainType(trigger.traderBType) {
		if trigger.sourceB != nil {
			tm.logger.Debugf("SourceB is already set as onchain trader, skipping subscription")
		} else {
			if err := trigger.subscribeOnchainForSource(symbol, false); err != nil {
				tm.removeTrigger(trigger.ID)
				return fmt.Errorf("failed to subscribe sourceB onchain: %w", err)
			}
			tm.logger.Debugf("Subscribed sourceB onchain for symbol: %s", symbol)
		}
	} else if trigger.sourceB != nil {
		_, _, bMarketType, _ := parseTraderTypeFromString(trigger.traderBType)
		
		// 添加重试机制，处理网络错误（如 broken pipe）
		maxRetries := 3
		var lastErr error
		for attempt := 1; attempt <= maxRetries; attempt++ {
			err := trigger.sourceB.Subscribe(exchangeSymbol, bMarketType)
			if err == nil {
				tm.logger.Debugf("Subscribed sourceB trader for symbol: %s (marketType=%s)", exchangeSymbol, bMarketType)
				break
			}
			
			lastErr = err
			errStr := err.Error()
			// 检查是否是网络错误（broken pipe, connection closed 等）
			isNetworkError := strings.Contains(errStr, "broken pipe") ||
				strings.Contains(errStr, "connection closed") ||
				strings.Contains(errStr, "write tcp") ||
				strings.Contains(errStr, "EOF")
			
			if isNetworkError && attempt < maxRetries {
				// 网络错误，等待后重试
				waitTime := time.Duration(attempt) * time.Second
				tm.logger.Warnf("订阅 sourceB trader 失败（尝试 %d/%d）: %v，%v 后重试...", 
					attempt, maxRetries, err, waitTime)
				time.Sleep(waitTime)
				continue
			}
			
			// 非网络错误或已达到最大重试次数
			tm.removeTrigger(trigger.ID)
			return fmt.Errorf("failed to subscribe sourceB trader after %d attempts: %w", attempt, err)
		}
		
		if lastErr != nil {
			tm.removeTrigger(trigger.ID)
			return fmt.Errorf("failed to subscribe sourceB trader: %w", lastErr)
		}
	}

	return nil
}

// addBrickMovingTriggerInternal 内部方法，处理 BrickMovingTrigger 的添加和订阅
func (tm *TriggerManager) addBrickMovingTriggerInternal(symbol string, trigger *BrickMovingTrigger) error {
	tm.triggers.Store(trigger.ID, trigger)
	// event bus 与 CEX 使用带计价币的 symbol（如 RAVEUSDT），以匹配交易所 ticker；token 映射常只有 base（RAVE）
	exchangeSymbol := normalizeSymbolForExchange(symbol)

	// 订阅 sourceA 的价格事件（如果是交易所类型）
	if !isOnchainType(trigger.traderAType) && trigger.sourceA != nil {
		_, _, aMarketType, _ := parseTraderTypeFromString(trigger.traderAType)
		if aMarketType == "" {
			aMarketType = "futures" // 默认使用 futures
		}
		tm.subExchangePriceEventBus.subscribe(exchangeSymbol, aMarketType, trigger.ID)
	}

	// 订阅 sourceB 的价格事件（如果是交易所类型）
	if !isOnchainType(trigger.traderBType) && trigger.sourceB != nil {
		_, _, bMarketType, _ := parseTraderTypeFromString(trigger.traderBType)
		if bMarketType == "" {
			bMarketType = "futures" // 默认使用 futures
		}
		tm.subExchangePriceEventBus.subscribe(exchangeSymbol, bMarketType, trigger.ID)
	}

	// 处理 sourceA 的订阅
	if isOnchainType(trigger.traderAType) {
		// sourceA 是链上类型，BrickMovingTrigger 已经在创建时设置了 sourceA
		if trigger.sourceA != nil {
			tm.logger.Debugf("SourceA is already set as onchain trader for BrickMovingTrigger")
		}
	} else if trigger.sourceA != nil {
		_, _, aMarketType, _ := parseTraderTypeFromString(trigger.traderAType)
		if aMarketType == "" {
			aMarketType = "futures"
		}

		// 添加重试机制，处理网络错误
		maxRetries := 3
		var lastErr error
		for attempt := 1; attempt <= maxRetries; attempt++ {
			err := trigger.sourceA.Subscribe(exchangeSymbol, aMarketType)
			if err == nil {
				tm.logger.Debugf("Subscribed sourceA trader for BrickMovingTrigger symbol: %s (marketType=%s)", exchangeSymbol, aMarketType)
				break
			}

			lastErr = err
			errStr := err.Error()
			isNetworkError := strings.Contains(errStr, "broken pipe") ||
				strings.Contains(errStr, "connection closed") ||
				strings.Contains(errStr, "write tcp") ||
				strings.Contains(errStr, "EOF")

			if isNetworkError && attempt < maxRetries {
				waitTime := time.Duration(attempt) * time.Second
				tm.logger.Warnf("订阅 sourceA trader 失败（尝试 %d/%d）: %v，%v 后重试...",
					attempt, maxRetries, err, waitTime)
				time.Sleep(waitTime)
				continue
			}

			tm.removeTrigger(trigger.ID)
			return fmt.Errorf("failed to subscribe sourceA trader after %d attempts: %w", attempt, err)
		}

		if lastErr != nil {
			tm.removeTrigger(trigger.ID)
			return fmt.Errorf("failed to subscribe sourceA trader: %w", lastErr)
		}
	}

	// 处理 sourceB 的订阅
	if isOnchainType(trigger.traderBType) {
		if trigger.sourceB != nil {
			tm.logger.Debugf("SourceB is already set as onchain trader for BrickMovingTrigger")
		}
	} else if trigger.sourceB != nil {
		_, _, bMarketType, _ := parseTraderTypeFromString(trigger.traderBType)
		if bMarketType == "" {
			bMarketType = "futures"
		}

		// 添加重试机制，处理网络错误
		maxRetries := 3
		var lastErr error
		for attempt := 1; attempt <= maxRetries; attempt++ {
			err := trigger.sourceB.Subscribe(exchangeSymbol, bMarketType)
			if err == nil {
				tm.logger.Debugf("Subscribed sourceB trader for BrickMovingTrigger symbol: %s (marketType=%s)", exchangeSymbol, bMarketType)
				break
			}

			lastErr = err
			errStr := err.Error()
			isNetworkError := strings.Contains(errStr, "broken pipe") ||
				strings.Contains(errStr, "connection closed") ||
				strings.Contains(errStr, "write tcp") ||
				strings.Contains(errStr, "EOF")

			if isNetworkError && attempt < maxRetries {
				waitTime := time.Duration(attempt) * time.Second
				tm.logger.Warnf("订阅 sourceB trader 失败（尝试 %d/%d）: %v，%v 后重试...",
					attempt, maxRetries, err, waitTime)
				time.Sleep(waitTime)
				continue
			}

			tm.removeTrigger(trigger.ID)
			return fmt.Errorf("failed to subscribe sourceB trader after %d attempts: %w", attempt, err)
		}

		if lastErr != nil {
			tm.removeTrigger(trigger.ID)
			return fmt.Errorf("failed to subscribe sourceB trader: %w", lastErr)
		}
	}

	return nil
}

// removeTrigger 移除Trigger
func (tm *TriggerManager) removeTrigger(triggerId uint64) {
	// 1. 获取 Trigger 实例以拿到 symbol
	value, ok := tm.triggers.Load(triggerId)
	if !ok {
		return
	}

	// 支持普通 Trigger 和 BrickMovingTrigger
	var symbol string
	var traderAType, traderBType string

	if trigger, ok := value.(*Trigger); ok {
		symbol = trigger.symbol
		traderAType = trigger.traderAType
		traderBType = trigger.traderBType
		if err := trigger.Stop(); err != nil {
			tm.logger.Warnf("Stop trigger %d (%s) before removal: %v", triggerId, symbol, err)
		}
	} else if bmt, ok := value.(*BrickMovingTrigger); ok {
		symbol = bmt.symbol
		traderAType = bmt.traderAType
		traderBType = bmt.traderBType
		if err := bmt.Stop(); err != nil {
			tm.logger.Warnf("Stop brick-moving trigger %d (%s) before removal: %v", triggerId, symbol, err)
		}
	} else {
		tm.triggers.Delete(triggerId)
		return
	}

	exchangeSymbol := normalizeSymbolForExchange(symbol)

	// 2. 从 triggers 映射中删除
	tm.triggers.Delete(triggerId)

	// 3. 取消订阅（与 add 时一致，使用带 USDT 的 symbol 和对应的 marketType）
	if !isOnchainType(traderAType) {
		_, _, aMarketType, _ := parseTraderTypeFromString(traderAType)
		if aMarketType == "" {
			aMarketType = "futures"
		}
		tm.subExchangePriceEventBus.unsubscribe(exchangeSymbol, aMarketType, triggerId)
	}
	if !isOnchainType(traderBType) {
		_, _, bMarketType, _ := parseTraderTypeFromString(traderBType)
		if bMarketType == "" {
			bMarketType = "futures"
		}
		tm.subExchangePriceEventBus.unsubscribe(exchangeSymbol, bMarketType, triggerId)
	}

	tm.UnsubscribeHedgingPriceForTrigger(triggerId)
}

// SubscribeHedgingPrice 为该 trigger 的套保交易所订阅合约价并写入缓存 key；保存套保配置时调用
func (tm *TriggerManager) SubscribeHedgingPrice(triggerSymbol, triggerIDStr, exchangeType string) {
	if triggerSymbol == "" || exchangeType == "" {
		return
	}
	triggerId, err := strconv.ParseUint(triggerIDStr, 10, 64)
	if err != nil {
		tm.logger.Warnf("SubscribeHedgingPrice: invalid triggerId %s: %v", triggerIDStr, err)
		return
	}
	key := buildHedgingKey(triggerSymbol, triggerIDStr, exchangeType)
	tm.hedgingMu.Lock()
	tm.hedgingPriceSubs[key] = struct{}{}
	if tm.triggerIDToHedgingKeys[triggerId] == nil {
		tm.triggerIDToHedgingKeys[triggerId] = make([]string, 0, 1)
	}
	tm.triggerIDToHedgingKeys[triggerId] = append(tm.triggerIDToHedgingKeys[triggerId], key)
	tm.hedgingMu.Unlock()

	et := constants.ExchangeType(strings.ToLower(strings.TrimSpace(exchangeType)))
	tr := tm.getExchangeSourceInternal(et)
	if tr == nil {
		tm.logger.Warnf("SubscribeHedgingPrice: no trader for exchangeType %s", exchangeType)
		return
	}
	symbol := normalizeSymbolForExchange(triggerSymbol)
	if err := tr.Subscribe(symbol, "futures"); err != nil {
		tm.logger.Warnf("SubscribeHedgingPrice: trader Subscribe(%s, futures) failed: %v", symbol, err)
	}
}

// GetHedgingPrice 返回该 trigger 的套保所合约最新 bid/ask；无数据时返回 0,0
func (tm *TriggerManager) GetHedgingPrice(triggerSymbol, triggerIDStr, exchangeType string) (bid, ask float64) {
	key := buildHedgingKey(triggerSymbol, triggerIDStr, exchangeType)
	tm.hedgingMu.RLock()
	t := tm.lastHedgingTicker[key]
	tm.hedgingMu.RUnlock()
	if t != nil {
		return t.BidPrice, t.AskPrice
	}
	return 0, 0
}

// UnsubscribeHedgingPriceForTrigger 删除该 trigger 的所有套保价订阅与缓存，removeTrigger 时调用
func (tm *TriggerManager) UnsubscribeHedgingPriceForTrigger(triggerId uint64) {
	tm.hedgingMu.Lock()
	keys := tm.triggerIDToHedgingKeys[triggerId]
	for _, k := range keys {
		delete(tm.hedgingPriceSubs, k)
		delete(tm.lastHedgingTicker, k)
	}
	delete(tm.triggerIDToHedgingKeys, triggerId)
	tm.hedgingMu.Unlock()
}

// run 启动管理器
func (tm *TriggerManager) run() {
	tm.routineGroup.Go(tm.handlePriceMsgChan)
}

// stop 停止管理器
func (tm *TriggerManager) stop() {
	tm.cancel()
}

// Run 启动管理器（公开方法）
func (tm *TriggerManager) Run() {
	tm.run()
}

// Stop 停止管理器（公开方法）
func (tm *TriggerManager) Stop() {
	tm.stop()
	tm.triggerContextCancel()
}

// StartAllTriggers 启动所有 trigger 的下单循环
func (tm *TriggerManager) StartAllTriggers() error {
	var lastErr error
	ctx := tm.triggerContext
	tm.triggers.Range(func(key, value interface{}) bool {
		if trigger, ok := value.(*Trigger); ok {
			if err := trigger.Start(ctx); err != nil {
				tm.logger.Errorf("Error starting trigger %d: %v", trigger.ID, err)
				lastErr = err
			} else {
				tm.logger.Infof("Trigger %d started successfully", trigger.ID)
			}
		} else if bmt, ok := value.(*BrickMovingTrigger); ok {
			if err := bmt.Start(ctx); err != nil {
				tm.logger.Errorf("Error starting brick-moving trigger %d: %v", bmt.ID, err)
				lastErr = err
			} else {
				tm.logger.Infof("BrickMovingTrigger %d started successfully", bmt.ID)
			}
		}
		return true
	})
	return lastErr
}

// StopAllTriggers 停止所有 trigger
func (tm *TriggerManager) StopAllTriggers() error {
	var lastErr error
	tm.triggers.Range(func(key, value interface{}) bool {
		if trigger, ok := value.(*Trigger); ok {
			if err := trigger.Stop(); err != nil {
				lastErr = err
			}
		} else if bmt, ok := value.(*BrickMovingTrigger); ok {
			if err := bmt.Stop(); err != nil {
				lastErr = err
			}
		}
		return true
	})
	return lastErr
}

// GetTrigger 实现 proto.TriggerManager 接口
func (tm *TriggerManager) GetTrigger(symbol string) (proto.Trigger, error) {
	var found proto.Trigger
	tm.triggers.Range(func(key, value interface{}) bool {
		var triggerSymbol string
		if t, ok := value.(*Trigger); ok {
			triggerSymbol = t.symbol
		} else if bmt, ok := value.(*BrickMovingTrigger); ok {
			triggerSymbol = bmt.symbol
		} else {
			return true
		}

		if triggerSymbol == symbol {
			if t, ok := value.(*Trigger); ok {
				found = t
			} else if bmt, ok := value.(*BrickMovingTrigger); ok {
				found = bmt
			}
			return false // 停止遍历
		}
		return true
	})
	if found == nil {
		return nil, fmt.Errorf("trigger not found for symbol: %s", symbol)
	}
	return found, nil
}

// getTriggerInternal 内部方法，返回具体类型（仅用于普通 Trigger）
func (tm *TriggerManager) getTriggerInternal(symbol string) (*Trigger, error) {
	var found *Trigger
	tm.triggers.Range(func(key, value interface{}) bool {
		if trigger, ok := value.(*Trigger); ok && trigger.symbol == symbol {
			found = trigger
			return false // 停止遍历
		}
		return true
	})
	if found == nil {
		return nil, fmt.Errorf("trigger not found for symbol: %s", symbol)
	}
	return found, nil
}

// GetTriggerByID 根据 ID 获取 trigger
func (tm *TriggerManager) GetTriggerByID(id uint64) (*Trigger, error) {
	value, ok := tm.triggers.Load(id)
	if !ok {
		return nil, fmt.Errorf("trigger not found for id: %d", id)
	}
	trigger, ok := value.(*Trigger)
	if !ok {
		return nil, fmt.Errorf("invalid trigger type for id: %d", id)
	}
	return trigger, nil
}

// GetTriggerByIDAsProto 根据 ID 获取 trigger（返回 proto.Trigger，支持 BrickMovingTrigger）
func (tm *TriggerManager) GetTriggerByIDAsProto(id uint64) (proto.Trigger, error) {
	value, ok := tm.triggers.Load(id)
	if !ok {
		return nil, fmt.Errorf("trigger not found for id: %d", id)
	}
	if t, ok := value.(*Trigger); ok {
		return t, nil
	}
	if bmt, ok := value.(*BrickMovingTrigger); ok {
		return bmt, nil
	}
	return nil, fmt.Errorf("invalid trigger type for id: %d", id)
}

// RemoveTriggerByID 根据 ID 移除 trigger
func (tm *TriggerManager) RemoveTriggerByID(id uint64) error {
	value, ok := tm.triggers.Load(id)
	if !ok {
		return fmt.Errorf("trigger not found for id: %d", id)
	}
	tm.removeTrigger(id)
	_ = value
	return nil
}

// GetAllTriggers 实现 proto.TriggerManager 接口
func (tm *TriggerManager) GetAllTriggers() []proto.Trigger {
	var triggers []proto.Trigger
	tm.triggers.Range(func(key, value interface{}) bool {
		if trigger, ok := value.(*Trigger); ok {
			triggers = append(triggers, trigger)
		} else if bmt, ok := value.(*BrickMovingTrigger); ok {
			triggers = append(triggers, bmt)
		}
		return true
	})
	return triggers
}

// getAllTriggersInternal 内部方法，返回具体类型切片（仅用于普通 Trigger）
func (tm *TriggerManager) getAllTriggersInternal() []*Trigger {
	var triggers []*Trigger
	tm.triggers.Range(func(key, value interface{}) bool {
		if trigger, ok := value.(*Trigger); ok {
			triggers = append(triggers, trigger)
		}
		return true
	})
	return triggers
}

// RemoveTrigger 移除指定的 trigger（支持 Trigger 和 BrickMovingTrigger）
func (tm *TriggerManager) RemoveTrigger(symbol string) error {
	tg, err := tm.GetTrigger(symbol)
	if err != nil {
		return err
	}
	tm.removeTrigger(tg.GetID())
	return nil
}

// GetTriggerContext 获取 trigger 上下文（用于启动 trigger）
func (tm *TriggerManager) GetTriggerContext() context.Context {
	return tm.triggerContext
}

// GetNextID 获取下一个 ID（用于创建新的 trigger）
func (tm *TriggerManager) GetNextID() uint64 {
	return tm.idGen.NextId()
}

// handlePriceMsgChan 处理价格消息队列
func (tm *TriggerManager) handlePriceMsgChan() {
	for {
		select {
		case <-tm.context.Done():
			return
		case exchangePrice := <-tm.exchangePriceMsgChan:
			symbol := exchangePrice.symbol
			marketType := exchangePrice.marketType
			if marketType == "" {
				marketType = "futures" // 默认使用 futures
			}
			triggerPriceData := TriggerPriceData{ExchangePriceMsg: exchangePrice}
			etStr := string(exchangePrice.exchangeType)

			// 使用 symbol+marketType 作为 key 查找订阅的 trigger
			key := buildKey(symbol, marketType)
			tm.subExchangePriceEventBus.mu.RLock()
			triggerIds := tm.subExchangePriceEventBus.subEventBus[key]
			tm.subExchangePriceEventBus.mu.RUnlock()

			for _, tickerId := range triggerIds {
				triggerOrigin, ok := tm.triggers.Load(tickerId)
				if !ok {
					tm.logger.Warnf("tm.triggers.Load(ticker) no exist, tickerId:%v", tickerId)
					continue
				}

				// 支持普通 Trigger 和 BrickMovingTrigger
				var traderAType, traderBType string
				var sourceA, sourceB trader.Trader
				var sourceAPriceChan, sourceBPriceChan chan TriggerPriceData

				if trigger, ok := triggerOrigin.(*Trigger); ok {
					traderAType = trigger.traderAType
					traderBType = trigger.traderBType
					sourceA = trigger.sourceA
					sourceB = trigger.sourceB
					sourceAPriceChan = trigger.sourceAPriceChan
					sourceBPriceChan = trigger.sourceBPriceChan
				} else if bmt, ok := triggerOrigin.(*BrickMovingTrigger); ok {
					traderAType = bmt.traderAType
					traderBType = bmt.traderBType
					sourceA = bmt.sourceA
					sourceB = bmt.sourceB
					sourceAPriceChan = bmt.sourceAPriceChan
					sourceBPriceChan = bmt.sourceBPriceChan
				} else {
					continue
				}

				aStr, _, aMarketType, _ := parseTraderTypeFromString(traderAType)
				bStr, _, bMarketType, _ := parseTraderTypeFromString(traderBType)
				
				// 按 exchangeType 和 marketType 路由：只把该交易所、该市场类型的 ticker 发给以其为 source 的 channel
				if sourceA != nil {
					if _, ok := sourceA.(trader.OnchainTrader); !ok {
						// 匹配交易所类型和市场类型
						if etStr == aStr && marketType == aMarketType {
							select {
							case sourceAPriceChan <- triggerPriceData:
							default:
							}
						}
					}
				}
				if sourceB != nil {
					if _, ok := sourceB.(trader.OnchainTrader); !ok {
						// 匹配交易所类型和市场类型
						if etStr == bStr && marketType == bMarketType {
							select {
							case sourceBPriceChan <- triggerPriceData:
							default:
							}
						}
					}
				}
			}

			// 套保价缓存：收到 futures 时更新所有匹配 symbol+exchangeType 的 hedging key
			if marketType == "futures" {
				tm.hedgingMu.Lock()
				for k := range tm.hedgingPriceSubs {
					if strings.HasPrefix(k, symbol+":") && strings.HasSuffix(k, ":"+etStr) {
						tm.lastHedgingTicker[k] = exchangePrice.ticker
					}
				}
				tm.hedgingMu.Unlock()
			}
		}
	}
}
