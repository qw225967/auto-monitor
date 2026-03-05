package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"

	"auto-arbitrage/internal/onchain/bridge"
	"auto-arbitrage/internal/utils/logger"

	"go.uber.org/zap"
)

// Pipeline 自动充提管道
// 由一组有序的节点和它们之间的边配置组成。
// 跨链是「边」上的行为：当相邻两个链上节点属于不同链时，由 bridgeManager 执行跨链。
type Pipeline struct {
	mu sync.RWMutex

	id    string
	name  string
	nodes []Node
	// key: "fromID->toID"
	edges map[string]*EdgeConfig

	// 跨链由边触发时使用（链 A -> 链 B 的边）
	bridgeManager *bridge.Manager

	// 中间节点步执行器：stepIndex >= 1 时用其定时轮询余额、满足后回调执行本步；nil 时使用默认实现
	middleNodeRunner MiddleNodeStepRunner

	// 执行状态（正常流程：Run 从头到尾）
	status      PipelineStatus
	currentStep int
	lastError   string    // 最后一次错误信息
	lastRunEnd  time.Time // 上次 Run() 结束时间（不管成功/失败）

	// 当前步骤的真实阶段描述（轮询时返回，非静态推断）
	currentStepPhase string // submitting | waiting_confirm | ""（已进入下一步或完成）
	currentStepLabel string // 如 "交易所提币已发出，等待链上确认"

	// 余额转账流程状态（定时触发、与正常流程分开展示）
	balanceTransferMu       sync.RWMutex
	balanceTransferStatus   string // idle | running | completed | failed
	balanceTransferStep     int    // 1-based
	balanceTransferFromName string
	balanceTransferToName   string
	balanceTransferLastError string
	balanceTransferLastEnd  time.Time

	// 每步完成后写入的 txHash，供下一步「交易所提币前区块确认」使用（RunWhenReady / RunStep 读取 stepIndex-1）
	lastStepTxHash map[int]string

	// 执行上下文
	ctx    context.Context
	cancel context.CancelFunc

	// 日志
	logger *zap.SugaredLogger
}

// PipelineStatus 管道状态
type PipelineStatus string

const (
	PipelineStatusIdle      PipelineStatus = "idle"
	PipelineStatusRunning   PipelineStatus = "running"
	PipelineStatusPaused    PipelineStatus = "paused"
	PipelineStatusCompleted PipelineStatus = "completed"
	PipelineStatusFailed    PipelineStatus = "failed"
)

// NewPipeline 创建一个新的 Pipeline 实例
func NewPipeline(name string, nodes ...Node) *Pipeline {
	ctx, cancel := context.WithCancel(context.Background())
	log := logger.GetLoggerInstance().Named("Pipeline").Sugar()

	p := &Pipeline{
		id:          name, // 暂时使用 name 作为 id，后续可以接入雪花算法
		name:        name,
		nodes:       make([]Node, 0, len(nodes)),
		edges:       make(map[string]*EdgeConfig),
		status:                  PipelineStatusIdle,
		currentStep:             0,
		balanceTransferStatus:   "idle",
		lastStepTxHash:          make(map[int]string),
		ctx:                     ctx,
		cancel:                  cancel,
		logger:                  log,
	}

	for _, n := range nodes {
		if n != nil {
			p.nodes = append(p.nodes, n)
		}
	}
	return p
}

// ID 返回 Pipeline 唯一标识
func (p *Pipeline) ID() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.id
}

// Name 返回 Pipeline 名称
func (p *Pipeline) Name() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.name
}

// Nodes 返回当前节点列表的拷贝
func (p *Pipeline) Nodes() []Node {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Node, len(p.nodes))
	copy(out, p.nodes)
	return out
}

// Status 返回当前 Pipeline 状态
func (p *Pipeline) Status() PipelineStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
}

// LastRunEnd 返回上次 Run() 结束时间
func (p *Pipeline) LastRunEnd() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastRunEnd
}

// CanRun 检查当前 pipeline 是否允许运行：
//   - 如果正在运行（status == Running），返回 false
//   - 如果上次运行结束不到 minCooldown 时间，返回 false（防止循环触发）
func (p *Pipeline) CanRun(minCooldown time.Duration) (bool, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.status == PipelineStatusRunning {
		return false, "pipeline is already running"
	}
	if !p.lastRunEnd.IsZero() && time.Since(p.lastRunEnd) < minCooldown {
		return false, fmt.Sprintf("pipeline recently finished (%.0fs ago, cooldown %.0fs), please wait",
			time.Since(p.lastRunEnd).Seconds(), minCooldown.Seconds())
	}
	return true, ""
}

// SetBridgeManager 注入跨链管理器；当路径中存在「链->链」边时，执行跨链步骤会使用它。
func (p *Pipeline) SetBridgeManager(mgr *bridge.Manager) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bridgeManager = mgr
}

// getBridgeManager 返回跨链管理器（供 executor 使用，只读）
func (p *Pipeline) getBridgeManager() *bridge.Manager {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.bridgeManager
}

// getMiddleNodeRunner 返回中间节点步执行器，nil 时返回默认实现（供 executor 使用）
func (p *Pipeline) getMiddleNodeRunner() MiddleNodeStepRunner {
	p.mu.RLock()
	r := p.middleNodeRunner
	p.mu.RUnlock()
	if r == nil {
		return defaultMiddleNodeRunnerInstance
	}
	return r
}

// SetMiddleNodeRunner 设置中间节点步执行器；nil 表示使用默认实现（定时轮询余额后回调执行本步）
func (p *Pipeline) SetMiddleNodeRunner(r MiddleNodeStepRunner) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.middleNodeRunner = r
}

// GetLastStepTxHash 返回指定步完成后记录的 txHash，供下一步「交易所提币前区块确认」使用；无则返回空字符串。
func (p *Pipeline) GetLastStepTxHash(stepIndex int) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.lastStepTxHash == nil {
		return ""
	}
	return p.lastStepTxHash[stepIndex]
}

// SetLastStepTxHash 在指定步成功完成后写入 txHash。
func (p *Pipeline) SetLastStepTxHash(stepIndex int, txHash string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastStepTxHash == nil {
		p.lastStepTxHash = make(map[int]string)
	}
	p.lastStepTxHash[stepIndex] = txHash
}

// AddNode 在末尾追加节点
func (p *Pipeline) AddNode(node Node) {
	if node == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nodes = append(p.nodes, node)
}

// InsertNode 在指定位置插入节点
func (p *Pipeline) InsertNode(index int, node Node) error {
	if node == nil {
		return fmt.Errorf("node is nil")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if index < 0 || index > len(p.nodes) {
		return fmt.Errorf("index out of range")
	}

	p.nodes = append(p.nodes[:index], append([]Node{node}, p.nodes[index:]...)...)
	return nil
}

// RemoveNode 按节点 ID 移除节点
func (p *Pipeline) RemoveNode(nodeID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	idx := -1
	for i, n := range p.nodes {
		if n.GetID() == nodeID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("node %s not found", nodeID)
	}

	// 删除节点
	p.nodes = append(p.nodes[:idx], p.nodes[idx+1:]...)

	// 同时清理与该节点相关的边
	for key, edge := range p.edges {
		if edge.FromNodeID == nodeID || edge.ToNodeID == nodeID {
			delete(p.edges, key)
		}
	}

	return nil
}

// edgeKey 生成边的 key
func edgeKey(fromID, toID string) string {
	return fromID + "->" + toID
}

// SetEdgeConfig 设置两个节点之间的边配置
func (p *Pipeline) SetEdgeConfig(fromNodeID, toNodeID string, config *EdgeConfig) error {
	if config == nil {
		return fmt.Errorf("edge config is nil")
	}
	if fromNodeID == "" || toNodeID == "" {
		return fmt.Errorf("fromNodeID and toNodeID are required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	config.FromNodeID = fromNodeID
	config.ToNodeID = toNodeID
	p.edges[edgeKey(fromNodeID, toNodeID)] = config
	return nil
}

// GetEdgeConfig 获取两个节点之间的边配置
func (p *Pipeline) GetEdgeConfig(fromNodeID, toNodeID string) (*EdgeConfig, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	edge, ok := p.edges[edgeKey(fromNodeID, toNodeID)]
	return edge, ok
}

// SetFixedAmount 设置 Pipeline 中所有边的固定提币金额
// 这会将所有边的 AmountType 设置为 AmountTypeFixed，并设置 Amount 为指定的值
// 用于在整个流程中统一使用固定金额进行提币
//
// 使用示例：
//   pipeline := CreateTestPipeline_BinanceToBitget("BNB")
//   err := pipeline.SetFixedAmount(0.1) // 设置所有步骤都提币 0.1 BNB
//   if err != nil {
//       log.Fatal(err)
//   }
//   pipeline.Run()
//
// 或者在测试配置中设置：
//   testConfig := TestPipelineConfig{
//       FixedAmount: 0.1, // 会自动应用到所有边
//       // ... 其他配置
//   }
func (p *Pipeline) SetFixedAmount(amount float64) error {
	if amount <= 0 {
		return fmt.Errorf("fixed amount must be greater than 0, got: %.8f", amount)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// 遍历所有边，设置固定金额
	for key, edge := range p.edges {
		edge.AmountType = AmountTypeFixed
		edge.Amount = amount
		p.logger.Debugf("Set fixed amount for edge %s: %.8f", key, amount)
	}

	p.logger.Infof("Set fixed amount for all edges: %.8f", amount)
	return nil
}

// CurrentStep 返回当前执行步骤（从 1 开始，0 表示未开始）
func (p *Pipeline) CurrentStep() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.currentStep
}

// SetCurrentStepPhase 设置当前步骤的真实阶段与展示文案（供轮询返回）
func (p *Pipeline) SetCurrentStepPhase(phase, label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.currentStepPhase = phase
	p.currentStepLabel = label
}

// GetCurrentStepLabel 返回当前步骤的真实展示文案（轮询时用，空则回退到静态推断）
func (p *Pipeline) GetCurrentStepLabel() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.currentStepLabel
}

// GetLastError 返回最后一次错误信息
func (p *Pipeline) GetLastError() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastError
}

// SetLastError 设置错误信息
func (p *Pipeline) SetLastError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.lastError = err.Error()
	} else {
		p.lastError = ""
	}
}

// ResetBalanceTransferToIdle 主流程 Run 开始时重置余额转账状态，避免与上次 RunStep 的 completed/failed 混在一起
func (p *Pipeline) ResetBalanceTransferToIdle() {
	p.balanceTransferMu.Lock()
	defer p.balanceTransferMu.Unlock()
	p.balanceTransferStatus = "idle"
	p.balanceTransferStep = 0
	p.balanceTransferFromName = ""
	p.balanceTransferToName = ""
	p.balanceTransferLastError = ""
}

// GetBalanceTransferStatus 返回余额转账流程状态（idle|running|completed|failed），供展示与调度器判断。
func (p *Pipeline) GetBalanceTransferStatus() string {
	p.balanceTransferMu.RLock()
	defer p.balanceTransferMu.RUnlock()
	return p.balanceTransferStatus
}

// GetBalanceTransferState 返回余额转账流程的展示用状态（供前端在主流程旁括号展示）。
func (p *Pipeline) GetBalanceTransferState() (status string, step int, fromName, toName, lastError string, lastEnd time.Time) {
	p.balanceTransferMu.RLock()
	defer p.balanceTransferMu.RUnlock()
	return p.balanceTransferStatus, p.balanceTransferStep, p.balanceTransferFromName, p.balanceTransferToName,
		p.balanceTransferLastError, p.balanceTransferLastEnd
}
