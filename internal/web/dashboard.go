package web

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"auto-arbitrage/internal/automation"
	"auto-arbitrage/internal/onchain/bridge"
	"auto-arbitrage/internal/pipeline"
	"auto-arbitrage/internal/proto"
	"auto-arbitrage/internal/statistics"
	"auto-arbitrage/internal/statistics/monitor"
	"auto-arbitrage/internal/trigger/chain_token_registry"
	"auto-arbitrage/internal/trigger/contract_mapping"
	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/web/ws"
)

// Dashboard 提供 Web 管理界面
type Dashboard struct {
	triggerManager     proto.TriggerManager
	tokenMappingMgr    proto.TokenMappingManager
	contractMappingMgr proto.ContractMappingManager
	logger             *zap.SugaredLogger
	mu                 sync.RWMutex

	// 搬砖 Trigger 管理：仅用于在 Web 层区分「搬砖区」和「其他区域」的 Trigger 视图
	// 注意：当前只在内存中标记，进程重启后需要重新通过搬砖区创建 Trigger 才会再次出现在列表中。
	brickMovingMu      sync.RWMutex
	brickMovingSymbols map[string]struct{}

	// 搬砖 Pipeline 配置（按 triggerSymbol 存储 forward/backward 的 nodes+edges 及当前生效的 pipeline ID）
	brickMovingPipelineMu  sync.RWMutex
	brickMovingPipelines   map[string]*brickMovingPipelineConfig

	// 搬砖套保配置（按 symbol 存储，供开仓等使用）
	brickMovingHedgingMu   sync.RWMutex
	brickMovingHedgingCfgs map[string]*brickMovingHedgingConfig

	// 智能翻转充提运行时状态：key=pipelineConfigKey(symbol,triggerID)，翻转执行中时 true
	brickMovingInFlipModeMu sync.RWMutex
	brickMovingInFlipMode   map[string]bool

	// 智能翻转刚结束时的结果，供提币历史接口短时间返回 flipPipelines 状态（绿/红）；key=pipelineConfigKey
	lastFlipPipelineResultMu sync.RWMutex
	lastFlipPipelineResult   map[string]lastFlipResult

	// WebSocket Hub
	wsHub *ws.Hub

	// Token 映射自动同步相关
	syncCtx        context.Context
	syncCancel     context.CancelFunc
	syncMu         sync.RWMutex
	lastSyncTime   time.Time
	syncInProgress bool

	// 自动化管理器
	automationManager *automation.Manager

	// 跨链协议地址缓存：配置 trigger 后从 LayerZero 等 API 加载 OFT/代币地址，供 apply/run/route-probe 使用
	bridgeRegistryMu   sync.RWMutex
	bridgeOFTRegistry  *bridge.OFTRegistry
	bridgeRefreshMu    sync.Mutex // 串行化 refresh，避免并发写 OFT 注册表
}

// NewDashboard 创建 Web 仪表板
func NewDashboard(tm proto.TriggerManager, tmm proto.TokenMappingManager) *Dashboard {
	ctx, cancel := context.WithCancel(context.Background())
	hub := ws.NewHub()

	// 将 Hub 注入到 ExecutionMonitor
	monitor.GetExecutionMonitor().SetWSHub(hub)

	// 将 Hub 注入到 StatisticsManager
	statistics.GetStatisticsManager().SetWSHub(hub)

	// 获取合约映射管理器单例
	contractMappingMgr := contract_mapping.GetContractMappingManager()

	dashboard := &Dashboard{
		triggerManager:     tm,
		tokenMappingMgr:    tmm,
		contractMappingMgr: contractMappingMgr,
		logger:             logger.GetLoggerInstance().Named("WebDashboard").Sugar(),
		syncCtx:            ctx,
		syncCancel:         cancel,
		wsHub:              hub,
		brickMovingSymbols:    make(map[string]struct{}),
		brickMovingPipelines:   make(map[string]*brickMovingPipelineConfig),
		brickMovingHedgingCfgs: make(map[string]*brickMovingHedgingConfig),
		brickMovingInFlipMode:   make(map[string]bool),
		lastFlipPipelineResult: make(map[string]lastFlipResult),
	}

	// 初始化自动化管理器
	dashboard.automationManager = automation.NewManager(tm, dashboard)

	// 搬砖区配置不再从磁盘恢复，每次启动为空（不载入上次配置）

	// 加载全链 token 地址注册表
	if err := chain_token_registry.GetRegistry().LoadFromFile(); err != nil {
		dashboard.logger.Warnf("Load chain token registry: %v", err)
	}

	return dashboard
}

// Start 启动 Web 服务
func (d *Dashboard) Start(addr string) error {
	// 启动 WebSocket Hub
	go d.wsHub.Run()

	// 注册自动充提开关查询：余额转账（中间节点 RunStep）仅当该 pipeline 对应 trigger 的自动充提按钮开启时触发。
	// 不依赖 trigger 的 running/stop 状态，只信任自动充提按钮。
	// 智能翻转充提执行中时，暂停本 trigger 的余额转账。
	pipeline.RegisterAutoWithdrawChecker(func(symbol, triggerIDStr string) bool {
		key := pipelineConfigKey(symbol, triggerIDStr)
		d.brickMovingInFlipModeMu.RLock()
		inFlip := d.brickMovingInFlipMode[key]
		d.brickMovingInFlipModeMu.RUnlock()
		if inFlip {
			return false
		}
		d.brickMovingHedgingMu.RLock()
		defer d.brickMovingHedgingMu.RUnlock()
		h := d.brickMovingHedgingCfgs[key]
		return h != nil && h.AutoWithdrawEnabled
	})

	// Favicon 处理（避免 404 错误）
	http.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	// 首页（不需要日志中间件）
	http.HandleFunc("/", d.handleHomePage)

	// 交易量刷子页（不需要日志中间件）
	http.HandleFunc("/volume-scalper", d.handleVolumeScalperPage)

	// 搬砖详细信息页（需要在 /brick-moving 之前注册，因为路径匹配顺序）
	http.HandleFunc("/brick-moving/", d.handleBrickMovingDetailPage)

	// 搬砖区页（不需要日志中间件）
	http.HandleFunc("/brick-moving", d.handleBrickMovingPage)
	// 搬砖区币列表页（设计文档路径，与 /brick-moving 同内容）
	http.HandleFunc("/arbitrage/arbitrage_list", d.handleBrickMovingPage)

	// 自动化配置页（不需要日志中间件）
	http.HandleFunc("/automation", d.handleAutomationPage)

	// WebSocket 路由
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ws.ServeWs(d.wsHub, w, r)
	})

	// Trigger 详情页（不需要日志中间件）
	http.HandleFunc("/trigger/", d.handleTriggerDetailPage)

	// TokenMapping API（应用日志中间件）
	http.HandleFunc("/api/token-mappings", d.loggingMiddleware(d.handleTokenMappings))
	http.HandleFunc("/api/token-mapping", d.loggingMiddleware(d.handleTokenMapping))
	http.HandleFunc("/api/token-mapping/sync", d.loggingMiddleware(d.handleSyncTokenMappings))
	http.HandleFunc("/api/token-mapping/sync-status", d.loggingMiddleware(d.handleSyncStatus))

	// ContractMapping API（应用日志中间件）
	http.HandleFunc("/api/contract-mappings", d.loggingMiddleware(d.handleContractMappings))
	http.HandleFunc("/api/contract-mapping", d.loggingMiddleware(d.handleContractMapping))

	// Trigger API（应用日志中间件）
	http.HandleFunc("/api/triggers", d.loggingMiddleware(d.handleGetTriggers))
	http.HandleFunc("/api/trigger", d.loggingMiddleware(d.handleGetTrigger))
	http.HandleFunc("/api/trigger/create", d.loggingMiddleware(d.handleCreateTrigger))
	http.HandleFunc("/api/trigger/update", d.loggingMiddleware(d.handleUpdateTrigger))
	http.HandleFunc("/api/trigger/delete", d.loggingMiddleware(d.handleDeleteTrigger))
	http.HandleFunc("/api/trigger/config", d.loggingMiddleware(d.handleUpdateTriggerConfig))
	http.HandleFunc("/api/trigger/telegram-notification", d.loggingMiddleware(d.handleUpdateTelegramNotification))
	http.HandleFunc("/api/trigger/start", d.loggingMiddleware(d.handleStartTrigger))
	http.HandleFunc("/api/trigger/stop", d.loggingMiddleware(d.handleStopTrigger))

	// 实时数据 API（应用日志中间件）
	http.HandleFunc("/api/trigger/data", d.loggingMiddleware(d.handleGetTriggerData))
	// 持仓信息 API（应用日志中间件）
	http.HandleFunc("/api/trigger/position", d.loggingMiddleware(d.handleGetTriggerPosition))

	// 清空价差数据 API（应用日志中间件）
	http.HandleFunc("/api/trigger/clear-price-diffs", d.loggingMiddleware(d.handleClearPriceDiffs))

	// 统计数据 API（应用日志中间件）
	http.HandleFunc("/api/statistics", d.loggingMiddleware(d.handleGetStatistics))
	// 清空统计数据 API
	http.HandleFunc("/api/statistics/clear", d.loggingMiddleware(d.handleClearStatistics))
	// 执行监控 API（应用日志中间件）
	http.HandleFunc("/api/monitor/executions", d.loggingMiddleware(d.handleGetExecutionLogs))

	// 钱包信息 API（应用日志中间件）
	http.HandleFunc("/api/wallet", d.loggingMiddleware(d.handleGetWallet))
	http.HandleFunc("/api/wallet/history", d.loggingMiddleware(d.handleGetWalletHistory))

	// 系统配置 API（应用日志中间件）
	http.HandleFunc("/api/config", d.loggingMiddleware(d.handleConfig))

	// 自动化 API（应用日志中间件）
	http.HandleFunc("/api/automation/config", d.loggingMiddleware(d.handleAutomationConfig))
	http.HandleFunc("/api/automation/status", d.loggingMiddleware(d.handleAutomationStatus))
	http.HandleFunc("/api/automation/control", d.loggingMiddleware(d.handleAutomationControl))

	// 搬砖区 API（应用日志中间件）
	http.HandleFunc("/api/brick-moving/triggers", d.loggingMiddleware(d.handleGetBrickMovingTriggers))
	http.HandleFunc("/api/brick-moving/arbitrage-list", d.loggingMiddleware(d.handleGetBrickMovingArbitrageList))
	http.HandleFunc("/api/brick-moving/trigger/create", d.loggingMiddleware(d.handleCreateBrickMovingTrigger))
	http.HandleFunc("/api/brick-moving/trigger/config", d.loggingMiddleware(d.handleUpdateBrickMovingTriggerConfig))
	http.HandleFunc("/api/brick-moving/trigger/hedging", d.loggingMiddleware(d.handleBrickMovingTriggerHedging))
	http.HandleFunc("/api/brick-moving/trigger/smart-flip", d.loggingMiddleware(d.handleUpdateBrickMovingTriggerSmartFlip))
	http.HandleFunc("/api/brick-moving/trigger/auto-transfer", d.loggingMiddleware(d.handleUpdateBrickMovingTriggerAutoTransfer))
	http.HandleFunc("/api/brick-moving/trigger/start", d.loggingMiddleware(d.handleBrickMovingTriggerStart))
	http.HandleFunc("/api/brick-moving/trigger/stop", d.loggingMiddleware(d.handleBrickMovingTriggerStop))
	http.HandleFunc("/api/brick-moving/trigger/delete", d.loggingMiddleware(d.handleBrickMovingTriggerDelete))
	http.HandleFunc("/api/brick-moving/token-chains/scan", d.loggingMiddleware(d.handleScanTokenChains))
	http.HandleFunc("/api/brick-moving/token-chains", d.loggingMiddleware(d.handleGetTokenChains))
	http.HandleFunc("/api/brick-moving/trigger/open-position", d.loggingMiddleware(d.handleOpenPosition))
	http.HandleFunc("/api/brick-moving/trigger/close-position", d.loggingMiddleware(d.handleClosePosition))
	http.HandleFunc("/api/brick-moving/trigger/swap", d.loggingMiddleware(d.handleBrickMovingTriggerSwap))
	http.HandleFunc("/api/brick-moving/trigger/hedging-position", d.loggingMiddleware(d.handleGetHedgingPosition))
	http.HandleFunc("/api/brick-moving/trigger/fills", d.loggingMiddleware(d.handleGetTriggerFills))
	http.HandleFunc("/api/brick-moving/trigger/price-diff", d.loggingMiddleware(d.handleGetBrickMovingTriggerPriceDiff))
	http.HandleFunc("/api/brick-moving/pipelines", d.loggingMiddleware(d.handleGetBrickMovingPipelines))
	http.HandleFunc("/api/brick-moving/pipeline", d.loggingMiddleware(d.handleSaveBrickMovingPipeline))
	http.HandleFunc("/api/brick-moving/pipeline/apply", d.loggingMiddleware(d.handleApplyBrickMovingPipeline))
	http.HandleFunc("/api/brick-moving/pipeline/current", d.loggingMiddleware(d.handleGetBrickMovingPipelineCurrent))
	http.HandleFunc("/api/brick-moving/pipeline/run", d.loggingMiddleware(d.handleRunBrickMovingPipeline))
	http.HandleFunc("/api/brick-moving/pipeline/last-run", d.loggingMiddleware(d.handleLastRunBrickMovingPipeline))
	http.HandleFunc("/api/brick-moving/pipeline/balance", d.loggingMiddleware(d.handleGetPipelineBalance))
	http.HandleFunc("/api/brick-moving/route-probe", d.loggingMiddleware(d.handleRouteProbe))
	http.HandleFunc("/api/brick-moving/chains/search", d.loggingMiddleware(d.handleSearchChains))
	http.HandleFunc("/api/brick-moving/withdraw-history", d.loggingMiddleware(d.handleBrickMovingWithdrawHistory))

	// 充提币 API（应用日志中间件）
	http.HandleFunc("/api/exchange/deposit", d.loggingMiddleware(d.handleDeposit))
	http.HandleFunc("/api/exchange/withdraw", d.loggingMiddleware(d.handleWithdraw))
	http.HandleFunc("/api/exchange/deposit-history", d.loggingMiddleware(d.handleDepositHistory))
	http.HandleFunc("/api/exchange/withdraw-history", d.loggingMiddleware(d.handleWithdrawHistory))

	// 跨链兑换 API（应用日志中间件）
	http.HandleFunc("/api/bridge/token", d.loggingMiddleware(d.handleBridgeToken))
	http.HandleFunc("/api/bridge/status", d.loggingMiddleware(d.handleBridgeStatus))
	http.HandleFunc("/api/bridge/quote", d.loggingMiddleware(d.handleBridgeQuote))

	// Pipeline API（应用日志中间件）
	http.HandleFunc("/api/pipeline/create", d.loggingMiddleware(d.handleCreatePipeline))
	http.HandleFunc("/api/pipeline/run", d.loggingMiddleware(d.handleRunPipeline))
	http.HandleFunc("/api/pipeline/status", d.loggingMiddleware(d.handlePipelineStatus))
	http.HandleFunc("/api/pipeline/list", d.loggingMiddleware(d.handleListPipelines))
	http.HandleFunc("/api/pipeline/nodes", d.loggingMiddleware(d.handlePipelineNodes))
	http.HandleFunc("/api/pipeline/check-availability", d.loggingMiddleware(d.handleCheckAvailability))

	// 版本信息 API
	http.HandleFunc("/api/version", d.handleGetVersion)

	// 启动定时同步任务（每 5 分钟同步一次）
	go d.startAutoSync()

	// 后端自动充提轮询：按周期检查余额并触发 pipeline run（关页后仍执行）
	go d.runAutoWithdrawLoop()

	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			fmt.Printf("Web dashboard error: %v\n", err)
		}
	}()
	return nil
}

// Stop 停止 Web 服务
func (d *Dashboard) Stop() {
	if d.syncCancel != nil {
		d.syncCancel()
	}
}

// StartAutomation 启动自动化管理器
func (d *Dashboard) StartAutomation() error {
	if d.automationManager == nil {
		return fmt.Errorf("automation manager not initialized")
	}
	return d.automationManager.Start()
}

// StopAutomation 停止自动化管理器
func (d *Dashboard) StopAutomation() {
	if d.automationManager != nil {
		d.automationManager.Stop()
	}
}
