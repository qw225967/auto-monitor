package automation

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/proto"
	"auto-arbitrage/internal/utils/logger"
)

// Manager 自动化管理器
type Manager struct {
	client        *OpportunityClient
	triggerManager proto.TriggerManager
	dashboard     DashboardInterface
	config        *model.AutomationConfig
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
	logger        *zap.SugaredLogger
	running       bool
}

// DashboardInterface 定义Dashboard需要的方法接口
type DashboardInterface interface {
	CreateTriggerInternal(symbol, traderAType, traderBType string) error
	StopTriggerInternal(symbol string) error
}

// NewManager 创建新的自动化管理器
func NewManager(triggerManager proto.TriggerManager, dashboard DashboardInterface) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		client:        NewOpportunityClient(),
		triggerManager: triggerManager,
		dashboard:     dashboard,
		ctx:           ctx,
		cancel:        cancel,
		logger:        logger.GetLoggerInstance().Named("AutomationManager").Sugar(),
		running:       false,
	}
}

// Start 启动自动化任务
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("automation manager is already running")
	}

	cfg := config.GetGlobalConfig()
	if !cfg.Automation.Enabled {
		m.logger.Info("Automation is disabled, not starting")
		return nil
	}

	m.config = &cfg.Automation
	m.running = true

	// 启动定时任务
	go m.run()

	m.logger.Info("Automation manager started")
	return nil
}

// Stop 停止自动化任务
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return
	}

	m.cancel()
	m.running = false
	m.logger.Info("Automation manager stopped")
}

// run 运行定时拉取任务
func (m *Manager) run() {
	ticker := time.NewTicker(m.config.PollInterval)
	defer ticker.Stop()

	// 立即执行一次
	m.processOpportunities()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.processOpportunities()
		}
	}
}

// processOpportunities 处理机会列表
func (m *Manager) processOpportunities() {
	m.mu.RLock()
	cfg := *m.config
	m.mu.RUnlock()

	// 拉取机会列表
	opportunities, err := m.client.FetchOpportunities(cfg.APIEndpoints)
	if err != nil {
		m.logger.Errorf("Failed to fetch opportunities: %v", err)
		return
	}

	if len(opportunities) == 0 {
		m.logger.Debug("No opportunities found")
		return
	}

	m.logger.Infof("Fetched %d opportunities", len(opportunities))

	// 机会去重：按symbol分组，每个symbol只保留利润最高的机会
	bestOpportunities := m.selectBestOpportunityBySymbol(opportunities)

	// 获取当前运行的所有trigger
	currentTriggers := m.triggerManager.GetAllTriggers()
	currentSymbols := make(map[string]proto.Trigger)
	for _, tg := range currentTriggers {
		currentSymbols[tg.GetSymbol()] = tg
	}

	// 处理需要创建的交易
	for symbol, opp := range bestOpportunities {
		// 检查交易对类型是否在允许列表中
		if !m.isTraderTypeAllowed(opp.TraderAType, cfg.AllowedTraderTypes) ||
			!m.isTraderTypeAllowed(opp.TraderBType, cfg.AllowedTraderTypes) {
			m.logger.Debugf("Skipping opportunity %s: trader type not allowed", symbol)
			continue
		}

		// 检查利润阈值
		if opp.Profit < cfg.ProfitThreshold {
			m.logger.Debugf("Skipping opportunity %s: profit %.4f < threshold %.4f", symbol, opp.Profit, cfg.ProfitThreshold)
			continue
		}

		// 检查状态
		if opp.Status != "active" {
			m.logger.Debugf("Skipping opportunity %s: status is %s", symbol, opp.Status)
			continue
		}

		_, exists := currentSymbols[symbol]
		if exists {
			// 已存在，检查是否需要切换（新机会利润更高）
			// 简化处理：如果新机会利润更高，直接切换
			// 注意：这里假设新机会利润更高，实际应该从trigger获取当前利润
			// 为了简化，我们直接切换（实际应该比较利润）
			m.logger.Infof("Trigger already exists for %s, skipping creation", symbol)
			// TODO: 实现利润比较逻辑，如果新机会利润更高则切换
		} else {
			// 不存在，创建新交易
			if err := m.dashboard.CreateTriggerInternal(symbol, opp.TraderAType, opp.TraderBType); err != nil {
				m.logger.Errorf("Failed to create trigger for %s: %v", symbol, err)
				continue
			}
			m.logger.Infof("Successfully created trigger for %s", symbol)
		}
	}

	// 处理需要停止的交易
	for symbol := range currentSymbols {
		// 检查是否在机会列表中
		opp, exists := bestOpportunities[symbol]
		if !exists {
			// 不在列表中，停止交易
			m.logger.Infof("Stopping trigger for %s: not in opportunity list", symbol)
			if err := m.dashboard.StopTriggerInternal(symbol); err != nil {
				m.logger.Errorf("Failed to stop trigger for %s: %v", symbol, err)
			}
			continue
		}

		// 检查利润是否低于阈值
		if opp.Profit < cfg.ProfitThreshold {
			m.logger.Infof("Stopping trigger for %s: profit %.4f < threshold %.4f", symbol, opp.Profit, cfg.ProfitThreshold)
			if err := m.dashboard.StopTriggerInternal(symbol); err != nil {
				m.logger.Errorf("Failed to stop trigger for %s: %v", symbol, err)
			}
			continue
		}

		// 检查状态
		if opp.Status != "active" {
			m.logger.Infof("Stopping trigger for %s: status is %s", symbol, opp.Status)
			if err := m.dashboard.StopTriggerInternal(symbol); err != nil {
				m.logger.Errorf("Failed to stop trigger for %s: %v", symbol, err)
			}
			continue
		}

		// 检查交易对类型是否在允许列表中
		if !m.isTraderTypeAllowed(opp.TraderAType, cfg.AllowedTraderTypes) ||
			!m.isTraderTypeAllowed(opp.TraderBType, cfg.AllowedTraderTypes) {
			m.logger.Infof("Stopping trigger for %s: trader type not allowed", symbol)
			if err := m.dashboard.StopTriggerInternal(symbol); err != nil {
				m.logger.Errorf("Failed to stop trigger for %s: %v", symbol, err)
			}
			continue
		}
	}
}

// selectBestOpportunityBySymbol 按symbol选择最高利润机会
func (m *Manager) selectBestOpportunityBySymbol(opportunities []model.ArbitrageOpportunity) map[string]model.ArbitrageOpportunity {
	bestMap := make(map[string]model.ArbitrageOpportunity)
	for _, opp := range opportunities {
		existing, exists := bestMap[opp.Symbol]
		if !exists || opp.Profit > existing.Profit {
			bestMap[opp.Symbol] = opp
		}
	}
	return bestMap
}

// isTraderTypeAllowed 检查交易对类型是否在允许列表中
func (m *Manager) isTraderTypeAllowed(traderType string, allowedTypes []string) bool {
	if len(allowedTypes) == 0 {
		return true // 如果未配置白名单，允许所有类型
	}

	// 解析基础类型
	baseType, _, _, err := parseTraderType(traderType)
	if err != nil {
		m.logger.Warnf("Failed to parse trader type %s: %v", traderType, err)
		return false
	}

	// 特殊处理：onchain 和 chain 都表示链上
	if baseType == "onchain" {
		baseType = "chain"
	}

	// 检查是否在允许列表中
	for _, allowed := range allowedTypes {
		allowedNormalized := strings.ToLower(allowed)
		if baseType == allowedNormalized || (baseType == "onchain" && allowedNormalized == "chain") {
			return true
		}
	}

	return false
}

// parseTraderType 解析交易对类型字符串
func parseTraderType(traderType string) (traderTypeStr, chainId, marketType string, err error) {
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

// getCurrentTriggerProfit 获取当前运行交易的利润
func (m *Manager) getCurrentTriggerProfit(symbol string, tg proto.Trigger) (float64, error) {
	// 尝试从trigger状态获取利润信息
	// 如果trigger没有直接提供利润信息，可以从机会列表中查找
	// 这里简化处理，返回0表示无法获取
	// 实际实现中，可以从trigger的统计数据或其他地方获取
	return 0, nil
}

// UpdateConfig 更新配置
func (m *Manager) UpdateConfig(cfg model.AutomationConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.config = &cfg
	m.logger.Info("Automation config updated")
}

// GetStatus 获取运行状态
func (m *Manager) GetStatus() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return map[string]interface{}{
		"running": m.running,
		"enabled": m.config != nil && m.config.Enabled,
	}
}
