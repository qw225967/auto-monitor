package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"

	"github.com/ethereum/go-ethereum/common"
)

func debugLogAgent(location, message string, data map[string]interface{}, hypothesisId string) {}

// StepResult 单步执行结果
type StepResult struct {
	FromNodeID string
	ToNodeID   string
	Success    bool
	TxHash     string // 交易哈希或提币ID
	Error      error
	Duration   time.Duration
}

// MiddleNodeStepRunner 中间节点步执行器：在限定时间内用定时器轮询余额，满足时调用回调执行本步提现。
// 用于缓解中间节点到账延迟导致本步「余额不足」立即失败的问题。
// prevTxHash 为上一步的 txHash，仅当本步为交易所提币时用于「完整区块确认」轮询。
type MiddleNodeStepRunner interface {
	RunWhenReady(ctx context.Context, p *Pipeline, fromNode, toNode Node, edge *EdgeConfig, executeOneStep func() (*StepResult, error), maxWait, checkInterval time.Duration, prevTxHash string) (*StepResult, error)
}

// defaultMiddleNodeRunner 默认实现：按 checkInterval 定时调用 calculateAmount，满足后调用 executeOneStep 一次并返回。
type defaultMiddleNodeRunner struct{}

var defaultMiddleNodeRunnerInstance MiddleNodeStepRunner = &defaultMiddleNodeRunner{}

const depositConfirmPollInterval = 30 * time.Second

func (d *defaultMiddleNodeRunner) RunWhenReady(ctx context.Context, p *Pipeline, fromNode, toNode Node, edge *EdgeConfig, executeOneStep func() (*StepResult, error), maxWait, checkInterval time.Duration, prevTxHash string) (*StepResult, error) {
	if maxWait <= 0 {
		maxWait = 30 * time.Minute
	}
	if checkInterval <= 0 {
		checkInterval = 15 * time.Second
	}
	deadline := time.Now().Add(maxWait)
	p.logger.Infof("Pipeline %s: middle node step waiting for sufficient balance (from %s to %s), maxWait=%v, checkInterval=%v",
		p.name, fromNode.GetID(), toNode.GetID(), maxWait, checkInterval)
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		amount, err := p.calculateAmount(fromNode, edge)
		if err == nil && amount > 0 {
			p.logger.Infof("Pipeline %s: middle node balance ready (amount=%.4f)", p.name, amount)
			// 仅当本步为交易所提币且存在上一步 txHash 时，等待「完整区块确认」后再执行
			if fromNode.GetType() == NodeTypeExchange && prevTxHash != "" {
				confirmDeadline := time.Now().Add(maxWait)
				confirmTicker := time.NewTicker(depositConfirmPollInterval)
				defer confirmTicker.Stop()
				for {
					confirmed, checkErr := fromNode.CheckDepositStatus(prevTxHash)
					if checkErr != nil {
						p.logger.Warnf("Pipeline %s: CheckDepositStatus(%s) error: %v", p.name, prevTxHash, checkErr)
					} else if confirmed {
						p.logger.Infof("Pipeline %s: deposit confirmed for txHash=%s, executing step", p.name, prevTxHash)
						return executeOneStep()
					}
					if time.Now().After(confirmDeadline) {
						p.logger.Warnf("Pipeline %s: timeout waiting for deposit confirmation (txHash=%s)", p.name, prevTxHash)
						return nil, fmt.Errorf("timeout waiting for deposit confirmation at exchange (txHash=%s)", prevTxHash)
					}
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-confirmTicker.C:
						// next poll
					case <-time.After(time.Until(confirmDeadline)):
						return nil, fmt.Errorf("timeout waiting for deposit confirmation at exchange (txHash=%s)", prevTxHash)
					}
				}
			}
			return executeOneStep()
		}
		if time.Now().After(deadline) {
			p.logger.Warnf("Pipeline %s: timeout waiting for sufficient balance at middle node (from %s to %s)", p.name, fromNode.GetID(), toNode.GetID())
			return nil, fmt.Errorf("timeout waiting for sufficient balance at middle node (from %s to %s)", fromNode.GetID(), toNode.GetID())
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			// next iteration
		case <-time.After(time.Until(deadline)):
			return nil, fmt.Errorf("timeout waiting for sufficient balance at middle node (from %s to %s)", fromNode.GetID(), toNode.GetID())
		}
	}
}

// Run 执行 Pipeline：正常流程，从头到尾完整执行所有充提节点（箭头闪烁、成功绿、失败红）。
// 并行/串行约定：不同 Pipeline 之间可并行（各用各的 p.mu/p.status）；同一 Pipeline 内节点之间串行（for 循环顺序执行每一步）。
func (p *Pipeline) Run() error {
	return p.runFullPipeline()
}

// ResumeFromStep 从指定步骤恢复执行（用于 Pipeline 失败后避免卡币，跳过已成功的步骤）。
// fromStep 为 0-based 的步骤索引（即 nodes[fromStep] -> nodes[fromStep+1]）。
func (p *Pipeline) ResumeFromStep(fromStep int) error {
	p.mu.Lock()
	if p.status == PipelineStatusRunning {
		p.mu.Unlock()
		return fmt.Errorf("pipeline is already running")
	}
	if fromStep < 0 || fromStep >= len(p.nodes)-1 {
		p.mu.Unlock()
		return fmt.Errorf("invalid step index %d (total steps: %d)", fromStep, len(p.nodes)-1)
	}
	p.status = PipelineStatusRunning
	p.currentStep = fromStep
	p.lastError = ""
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		if p.status == PipelineStatusRunning {
			p.status = PipelineStatusCompleted
		}
		p.lastRunEnd = time.Now()
		p.mu.Unlock()
	}()

	p.logger.Infof("Pipeline %s: Resuming from step %d/%d", p.name, fromStep+1, len(p.nodes)-1)

	for i := fromStep; i < len(p.nodes)-1; i++ {
		fromNode := p.nodes[i]
		toNode := p.nodes[i+1]

		p.mu.Lock()
		p.currentStep = i + 1
		p.mu.Unlock()

		p.logger.Infof("Pipeline %s: Step %d/%d (resumed): %s -> %s", p.name, i+1, len(p.nodes)-1, fromNode.GetName(), toNode.GetName())

		edge, hasEdge := p.GetEdgeConfig(fromNode.GetID(), toNode.GetID())
		var result *StepResult
		var err error

		if i > fromStep && hasEdge && edge != nil {
			maxWait := 30 * time.Minute
			checkInterval := 15 * time.Second
			prevStepTxHash := p.GetLastStepTxHash(i - 1)
			executeOneStep := func() (*StepResult, error) { return p.executeStep(fromNode, toNode, i, true) }
			runner := p.getMiddleNodeRunner()
			result, err = runner.RunWhenReady(p.ctx, p, fromNode, toNode, edge, executeOneStep, maxWait, checkInterval, prevStepTxHash)
		} else {
			result, err = p.executeStep(fromNode, toNode, i, false)
		}
		if err != nil {
			p.mu.Lock()
			p.status = PipelineStatusFailed
			p.lastError = fmt.Sprintf("step %d (resumed) failed: %v", i+1, err)
			p.mu.Unlock()
			return fmt.Errorf("step %d (resumed) failed: %w", i+1, err)
		}

		if result != nil && result.TxHash != "" {
			p.SetLastStepTxHash(i, result.TxHash)
		}
	}
	return nil
}

// GetCurrentStep 返回最后执行到的步骤（1-based），用于故障恢复时确定从哪一步继续
func (p *Pipeline) GetCurrentStep() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.currentStep
}

// runFullPipeline 内部实现：预检查 + 顺序执行每一步（中间节点步通过 RunWhenReady 等余额后执行）。单 pipeline 内串行。
func (p *Pipeline) runFullPipeline() error {
	p.mu.Lock()
	if p.status == PipelineStatusRunning {
		p.mu.Unlock()
		return fmt.Errorf("pipeline is already running")
	}
	p.status = PipelineStatusRunning
	p.currentStep = 0
	p.lastError = "" // 新一次运行开始时清除上次错误/警告，避免「余额不足」等旧提示一直残留
	p.mu.Unlock()

	p.ResetBalanceTransferToIdle() // 主流程 Run 负责全链路，重置余额转账状态避免与上次 RunStep 混在一起

	defer func() {
		p.mu.Lock()
		if p.status == PipelineStatusRunning {
			p.status = PipelineStatusCompleted
			p.lastError = "" // 成功完成时清空，便于下次运行前状态干净
		}
		p.lastRunEnd = time.Now() // 记录结束时间，用于防止循环触发
		p.mu.Unlock()
	}()

	// 检查节点数量
	if len(p.nodes) < 2 {
		return fmt.Errorf("pipeline must have at least 2 nodes")
	}

	// 1. 预检查：打印所有节点余额 + 仅对第一个源节点做余额充足性验证
	// 注意：多步 pipeline（如 A→B→C）中，中间节点 B 的余额在 step 1 完成后才到账，
	// 所以预检查只验证 step 1 的源节点（node[0]），后续步骤的余额在 executeStep 中实时检查。
	p.logger.Infof("Pipeline %s: Starting pre-check", p.name)
	for i, node := range p.nodes {
		balance, err := node.CheckBalance()
		if err != nil {
			p.logger.Warnf("Node %s (%s): CheckBalance failed: %v", node.GetID(), node.GetName(), err)
		} else {
			p.logger.Infof("Node %s (%s): Balance = %.4f %s", node.GetID(), node.GetName(), balance, node.GetAsset())
		}
		// 只对第一个源节点做余额 vs 需求的预检查
		// 余额不足时以 [WARN] 前缀标记 lastError，前端据此显示黄色警告而非红色错误
		if i == 0 && i < len(p.nodes)-1 {
			toNode := p.nodes[i+1]
			edge, hasEdge := p.GetEdgeConfig(node.GetID(), toNode.GetID())
			if hasEdge && edge != nil {
				requiredAmount, calcErr := p.calculateAmount(node, edge)
				if calcErr != nil {
					p.mu.Lock()
					p.status = PipelineStatusFailed
					p.lastError = fmt.Sprintf("[WARN] step 1 pre-check: calculate amount failed: %v", calcErr)
					p.mu.Unlock()
					return fmt.Errorf("step 1 (%s -> %s) pre-check failed: %w",
						node.GetName(), toNode.GetName(), calcErr)
				}
			// 当边配置了 Asset 且与节点资产不同时（如边为 USDT、节点为原币），金额单位不一致，跳过余额预检查，在 executeWithdrawStep 中按边资产检查
			edgeAsset := getEdgeAsset(edge, node)
			skipBalanceCheck := edgeAsset != "" && edgeAsset != node.GetAsset()
			// #region agent log
			debugLogAgent("Run:balancePreCheck", "balance vs required comparison", map[string]interface{}{
				"balance": balance, "requiredAmount": requiredAmount, "nodeAsset": node.GetAsset(),
				"nodeID": node.GetID(), "pipelineName": p.name, "edgeAsset": edgeAsset, "skipBalanceCheck": skipBalanceCheck,
				"willFail": !skipBalanceCheck && requiredAmount > 0 && balance < requiredAmount,
			}, "H9")
			// #endregion
			if !skipBalanceCheck && requiredAmount > 0 && balance < requiredAmount {
				p.mu.Lock()
				p.status = PipelineStatusFailed
				p.lastError = fmt.Sprintf("[WARN] 余额不足: 可用=%.4f, 需要=%.4f %s",
					balance, requiredAmount, node.GetAsset())
				p.mu.Unlock()
				return fmt.Errorf("step 1 (%s -> %s) insufficient balance: available=%.4f, required=%.4f %s",
					node.GetName(), toNode.GetName(), balance, requiredAmount, node.GetAsset())
			}
			p.logger.Infof("Pipeline %s: Step 1 balance pre-check passed: available=%.4f, required=%.4f %s (edgeAsset=%s)",
				p.name, balance, requiredAmount, node.GetAsset(), edgeAsset)
			}
		}
	}

	// 1.1 预检查跨链边：提前验证 bridge 条件（OFT 合约等），避免执行到中途才失败
	mgr := p.getBridgeManager()
	for i := 0; i < len(p.nodes)-1; i++ {
		fromNode := p.nodes[i]
		toNode := p.nodes[i+1]
		if !p.needsBridge(fromNode, toNode) {
			continue
		}
		if mgr == nil {
			p.mu.Lock()
			p.status = PipelineStatusFailed
			p.lastError = fmt.Sprintf("step %d requires bridge but bridge manager not set", i+1)
			p.mu.Unlock()
			return fmt.Errorf("step %d (%s -> %s) requires bridge but bridge manager not set",
				i+1, fromNode.GetName(), toNode.GetName())
		}
		fromChain := p.getChainID(fromNode)
		toChain := p.getChainID(toNode)
		edge, _ := p.GetEdgeConfig(fromNode.GetID(), toNode.GetID())
		protocol := ""
		if edge != nil {
			protocol = edge.BridgeProtocol
		}
		tokenSymbol := getEdgeAsset(edge, fromNode)
		bridgeErr := mgr.CheckBridgeReady(fromChain, toChain, tokenSymbol, protocol)
		// #region agent log
		debugLogAgent("Run:bridgePreCheck", "bridge pre-check result", map[string]interface{}{
			"step": i + 1, "fromChain": fromChain, "toChain": toChain,
			"tokenSymbol": tokenSymbol, "protocol": protocol,
			"fromNode": fromNode.GetID(), "toNode": toNode.GetID(),
			"error": fmt.Sprintf("%v", bridgeErr), "passed": bridgeErr == nil,
		}, "H10")
		// #endregion
		if bridgeErr != nil {
			p.mu.Lock()
			p.status = PipelineStatusFailed
			p.lastError = fmt.Sprintf("step %d bridge pre-check failed: %v", i+1, bridgeErr)
			p.mu.Unlock()
			return fmt.Errorf("step %d (%s -> %s) bridge pre-check failed: %w",
				i+1, fromNode.GetName(), toNode.GetName(), bridgeErr)
		}
		p.logger.Infof("Pipeline %s: Bridge pre-check passed for step %d (%s -> %s, token=%s)",
			p.name, i+1, fromNode.GetName(), toNode.GetName(), tokenSymbol)
	}

	// 2. 顺序执行每对相邻节点之间的转账
	// #region agent log
	debugLogAgent("Run:execLoopEntry", "entering execution loop", map[string]interface{}{
		"totalSteps": len(p.nodes) - 1, "pipelineName": p.name,
	}, "H12")
	// #endregion
	var prevStepTxHash string
	for i := 0; i < len(p.nodes)-1; i++ {
		fromNode := p.nodes[i]
		toNode := p.nodes[i+1]

		p.mu.Lock()
		p.currentStep = i + 1
		p.mu.Unlock()

		p.logger.Infof("Pipeline %s: Step %d/%d: %s -> %s", p.name, i+1, len(p.nodes)-1, fromNode.GetName(), toNode.GetName())

		var result *StepResult
		var err error
		if i >= 1 {
			// 中间节点步：通过 Runner 定时轮询余额（及交易所提币时的区块确认），满足后回调执行本步提现
			edge, hasEdge := p.GetEdgeConfig(fromNode.GetID(), toNode.GetID())
			if !hasEdge || edge == nil {
				edge = &EdgeConfig{
					AmountType:   AmountTypeAll,
					MaxWaitTime:  30 * time.Minute,
					CheckInterval: 10 * time.Second,
				}
			}
			maxWait := edge.MaxWaitTime
			checkInterval := edge.CheckInterval
			if maxWait <= 0 {
				maxWait = 30 * time.Minute
			}
			if checkInterval <= 0 {
				checkInterval = 15 * time.Second
			}
			stepIndex := i
			executeOneStep := func() (*StepResult, error) { return p.executeStep(fromNode, toNode, stepIndex, true) }
			runner := p.getMiddleNodeRunner()
			result, err = runner.RunWhenReady(p.ctx, p, fromNode, toNode, edge, executeOneStep, maxWait, checkInterval, prevStepTxHash)
		} else {
			result, err = p.executeStep(fromNode, toNode, i, false)
		}
		if err != nil {
			p.mu.Lock()
			p.status = PipelineStatusFailed
			errStr := err.Error()
			if strings.Contains(errStr, "insufficient") && strings.Contains(errStr, "balance") {
				p.lastError = "[WARN] 余额不足，已跳过本次提币，下次检测再试"
			} else {
				p.lastError = fmt.Sprintf("step %d failed: %v", i+1, err)
			}
			p.mu.Unlock()
			p.SetCurrentStepPhase("", "") // 失败时清除，避免轮询返回过时的「等待确认」
			return fmt.Errorf("step %d failed: %w", i+1, err)
		}

		p.SetCurrentStepPhase("", "") // 本步完成，下一步会重新设置
		prevStepTxHash = result.TxHash
		p.SetLastStepTxHash(i, result.TxHash)
		p.logger.Infof("Pipeline %s: Step %d completed: %s -> %s, txHash=%s, duration=%v",
			p.name, i+1, fromNode.GetName(), toNode.GetName(), result.TxHash, result.Duration)
	}

	p.logger.Infof("Pipeline %s: All steps completed successfully", p.name)
	return nil
}

// RunStep 执行单步（余额转账流程专用）：仅更新 balanceTransfer* 状态，不修改主流程 status/currentStep，供前端在主流程旁括号展示。
func (p *Pipeline) RunStep(stepIndex int) error {
	p.mu.Lock()
	nodes := p.nodes
	if stepIndex < 0 || stepIndex >= len(nodes)-1 {
		p.mu.Unlock()
		return fmt.Errorf("stepIndex %d out of range (nodes=%d)", stepIndex, len(nodes))
	}
	p.mu.Unlock()

	p.balanceTransferMu.Lock()
	if p.balanceTransferStatus == "running" {
		p.balanceTransferMu.Unlock()
		return fmt.Errorf("balance transfer already running")
	}
	p.balanceTransferStatus = "running"
	p.balanceTransferStep = stepIndex + 1
	p.balanceTransferFromName = nodes[stepIndex].GetName()
	p.balanceTransferToName = nodes[stepIndex+1].GetName()
	p.balanceTransferLastError = ""
	p.balanceTransferMu.Unlock()

	defer func() {
		p.balanceTransferMu.Lock()
		p.balanceTransferLastEnd = time.Now()
		p.balanceTransferMu.Unlock()
	}()

	fromNode := nodes[stepIndex]
	toNode := nodes[stepIndex+1]
	p.logger.Infof("Pipeline %s: RunStep(%d) 余额转账: %s -> %s", p.name, stepIndex+1, fromNode.GetName(), toNode.GetName())

	edge, hasEdge := p.GetEdgeConfig(fromNode.GetID(), toNode.GetID())
	if !hasEdge || edge == nil {
		edge = &EdgeConfig{
			AmountType:    AmountTypeAll,
			MaxWaitTime:   30 * time.Minute,
			CheckInterval: 10 * time.Second,
		}
	}
	maxWait := edge.MaxWaitTime
	checkInterval := edge.CheckInterval
	if maxWait <= 0 {
		maxWait = 30 * time.Minute
	}
	if checkInterval <= 0 {
		checkInterval = 15 * time.Second
	}
	// 中间节点余额转账：等待「有余额即可」再执行，执行时 forceAllBalance 会转全部；若用边的 AmountTypeFixed 会要求达到固定金额导致余额不足时一直等不到、永不发合约
	checkEdge := edge
	if edge.AmountType == AmountTypeFixed && edge.Amount > 0 {
		checkEdge = &EdgeConfig{
			AmountType:    AmountTypeAll,
			MaxWaitTime:   edge.MaxWaitTime,
			CheckInterval: edge.CheckInterval,
		}
		if checkEdge.MaxWaitTime <= 0 {
			checkEdge.MaxWaitTime = 30 * time.Minute
		}
		if checkEdge.CheckInterval <= 0 {
			checkEdge.CheckInterval = 15 * time.Second
		}
	}
	prevTxHash := p.GetLastStepTxHash(stepIndex - 1)
	executeOneStep := func() (*StepResult, error) { return p.executeStep(fromNode, toNode, stepIndex, true) }
	runner := p.getMiddleNodeRunner()
	result, err := runner.RunWhenReady(p.ctx, p, fromNode, toNode, checkEdge, executeOneStep, maxWait, checkInterval, prevTxHash)

	p.balanceTransferMu.Lock()
	if err != nil {
		p.balanceTransferStatus = "failed"
		p.balanceTransferLastError = err.Error()
	} else {
		p.balanceTransferStatus = "completed"
	}
	p.balanceTransferMu.Unlock()

	p.SetCurrentStepPhase("", "") // RunStep 完成（成功或失败）后清除，避免轮询返回过时状态

	if err != nil {
		return err
	}
	p.SetLastStepTxHash(stepIndex, result.TxHash)
	p.logger.Infof("Pipeline %s: 余额转账 Step %d completed: %s -> %s, txHash=%s", p.name, stepIndex+1, fromNode.GetName(), toNode.GetName(), result.TxHash)
	return nil
}

// getStepLabelsForExecutor 根据边类型返回步骤文案（提交中、等待确认、已完成），供 SetCurrentStepPhase 使用
func (p *Pipeline) getStepLabelsForExecutor(fromNode, toNode Node) (submitting, waitingConfirm, done string) {
	fromType := fromNode.GetType()
	toType := toNode.GetType()
	fromChain := p.getChainID(fromNode)
	toChain := p.getChainID(toNode)
	needsBridge := fromType == NodeTypeOnchain && toType == NodeTypeOnchain &&
		fromChain != "" && toChain != "" && fromChain != toChain

	if needsBridge {
		return "跨链中...", "跨链已执行，等待回款确认", "跨链协议已执行，跨链回款已收到"
	}
	if fromType == NodeTypeExchange && toType == NodeTypeOnchain {
		return "交易所提币中...", "交易所提币已发出，等待链上确认", "交易所提币已发出，链上已收到"
	}
	if fromType == NodeTypeExchange && toType == NodeTypeExchange {
		return "交易所提币中...", "交易所提币已发出，等待目标确认", "交易所提币已发出，目标已收到"
	}
	if fromType == NodeTypeOnchain && toType == NodeTypeExchange {
		return "链上充值中...", "链上充值已发出，等待交易所确认", "链上充值已发出，交易所已收到"
	}
	return "转账中...", "已发出，等待确认", "已到账"
}

// executeStep 执行单步转账（从 fromNode 到 toNode）
// forceAllBalance: 为 true 时（中间节点余额转账）强制转出全部可用余额，忽略 edge 的 AmountType/Amount
func (p *Pipeline) executeStep(fromNode, toNode Node, stepIndex int, forceAllBalance bool) (*StepResult, error) {
	startTime := time.Now()

	submittingLabel, waitingLabel, _ := p.getStepLabelsForExecutor(fromNode, toNode)
	p.SetCurrentStepPhase("submitting", submittingLabel)

	// #region agent log
	debugLogAgent("executor.go:executeStep:entry", "step start", map[string]interface{}{
		"stepIndex": stepIndex + 1, "fromID": fromNode.GetID(), "toID": toNode.GetID(),
		"fromName": fromNode.GetName(), "toName": toNode.GetName(), "forceAllBalance": forceAllBalance,
	}, "H1")
	// #endregion

	// 获取边配置
	edge, hasEdge := p.GetEdgeConfig(fromNode.GetID(), toNode.GetID())
	if !hasEdge {
		// 如果没有配置边，使用默认配置
		edge = &EdgeConfig{
			AmountType:   AmountTypeAll,
			MaxWaitTime:  30 * time.Minute,
			CheckInterval: 10 * time.Second,
		}
	}

	// 计算转账金额：中间节点余额转账时强制转出全部
	// 注意：当边资产与节点资产不同时（如 backward 用 USDT、节点为 binance-ZAMA），必须按边资产查余额，否则会错误得到 ZAMA 余额 0
	getAvailableForStep := func() (float64, string, error) {
		edgeAsset := getEdgeAsset(edge, fromNode)
		if fromNode.GetType() == NodeTypeExchange && edgeAsset != "" && edgeAsset != fromNode.GetAsset() {
			if ex, ok := fromNode.(*ExchangeNode); ok {
				saved := ex.cfg.Asset
				ex.cfg.Asset = edgeAsset
				bal, err := fromNode.GetAvailableBalance()
				ex.cfg.Asset = saved
				return bal, edgeAsset, err
			}
		}
		bal, err := fromNode.GetAvailableBalance()
		return bal, fromNode.GetAsset(), err
	}

	var amount float64
	var err error
	var assetUsed string
	if forceAllBalance {
		amount, assetUsed, err = getAvailableForStep()
		if err != nil {
			return nil, fmt.Errorf("get available balance failed: %w", err)
		}
		p.logger.Infof("Step %d: 余额转账强制转出全部, amount=%.4f %s", stepIndex+1, amount, assetUsed)
	} else {
		amount, err = p.calculateAmount(fromNode, edge)
		if err != nil {
			return nil, fmt.Errorf("calculate amount failed: %w", err)
		}
		assetUsed = getEdgeAsset(edge, fromNode)
		if assetUsed == "" {
			assetUsed = fromNode.GetAsset()
		}
	}
	if amount <= 0 {
		available, usedAsset, _ := getAvailableForStep()
		return nil, fmt.Errorf("calculated amount is zero or negative (available balance: %.4f %s, node: %s/%s)", 
			available, usedAsset, fromNode.GetID(), fromNode.GetName())
	}

	p.logger.Infof("Step %d: Transfer amount = %.4f %s", stepIndex+1, amount, assetUsed)

	// 根据节点类型选择执行策略
	var txHash string
	var bridgeID string

	// 判断是否需要跨链
	needsBridge := p.needsBridge(fromNode, toNode)

	// #region agent log
	debugLogAgent("executor.go:executeStep:afterNeedsBridge", "amount and needsBridge", map[string]interface{}{
		"stepIndex": stepIndex + 1, "amount": amount, "needsBridge": needsBridge,
		"fromChain": p.getChainID(fromNode), "toChain": p.getChainID(toNode),
	}, "H2")
	// #endregion

	if needsBridge {
		// 跨链场景：由边触发，通过 Pipeline 的 bridgeManager 执行。协议取自 edge.BridgeProtocol（layerzero/wormhole/auto），
		// OFT 地址与各链 RPC 由 Web 层在 SetBridgeManager 之前提前拉取并注入，见 web 层 refreshBridgeAddressesForSymbol 与 rpcURLs 构造。
		txHash, bridgeID, err = p.executeBridgeStep(fromNode, toNode, amount, edge)
	} else {
		// 普通场景：从源节点提币到目标节点
		txHash, err = p.executeWithdrawStep(fromNode, toNode, amount, edge)
	}

	if err != nil {
		return &StepResult{
			FromNodeID: fromNode.GetID(),
			ToNodeID:   toNode.GetID(),
			Success:    false,
			Error:      err,
			Duration:   time.Since(startTime),
		}, err
	}

	// 等待确认：更新为真实状态，供轮询返回
	p.SetCurrentStepPhase("waiting_confirm", waitingLabel)

	// 等待确认
	p.logger.Infof("Step %d: Waiting for confirmation, txHash=%s", stepIndex+1, txHash)

	// #region agent log
	debugLogAgent("executor.go:executeStep:beforeWait", "wait confirmation", map[string]interface{}{
		"stepIndex": stepIndex + 1, "txHash": txHash, "bridgeID": bridgeID, "hasBridgeID": bridgeID != "",
	}, "H5")
	// #endregion

	// 当边配置的资产与交易所节点默认资产不同时（如边为 USDT、节点为原币），确认阶段需按边资产查询提币状态（含 交易所→链上 与 交易所→交易所，否则 OKX 用 ZAMA 查历史会找不到刚提交的 USDT 提现）
	edgeAsset := getEdgeAsset(edge, fromNode)
	needRestoreAsset := false
	var savedAsset string
	if fromNode.GetType() == NodeTypeExchange && edgeAsset != "" && edgeAsset != fromNode.GetAsset() {
		if exchangeNode, ok := fromNode.(*ExchangeNode); ok {
			savedAsset = exchangeNode.cfg.Asset
			exchangeNode.cfg.Asset = edgeAsset
			needRestoreAsset = true
			p.logger.Infof("Confirmation: temporarily switched exchange asset to %s for withdraw status check (original=%s)", edgeAsset, savedAsset)
		}
	}

	// #region agent log
	debugLogAgent("executeStep:confirmAssetSwitch", "confirmation phase asset state", map[string]interface{}{
		"edgeAsset": edgeAsset, "needRestoreAsset": needRestoreAsset,
		"savedAsset": savedAsset, "txHash": txHash,
		"fromType": string(fromNode.GetType()), "toType": string(toNode.GetType()),
	}, "H2")
	// #endregion

	confirmed, err := p.waitForConfirmation(fromNode, toNode, txHash, bridgeID, edge, amount)

	// 恢复原始 Asset
	if needRestoreAsset {
		if exchangeNode, ok := fromNode.(*ExchangeNode); ok {
			exchangeNode.cfg.Asset = savedAsset
			p.logger.Infof("Confirmation: restored exchange asset to %s", savedAsset)
		}
	}

	if err != nil {
		return &StepResult{
			FromNodeID: fromNode.GetID(),
			ToNodeID:   toNode.GetID(),
			Success:    false,
			TxHash:     txHash,
			Error:      err,
			Duration:   time.Since(startTime),
		}, err
	}
	if !confirmed {
		return &StepResult{
			FromNodeID: fromNode.GetID(),
			ToNodeID:   toNode.GetID(),
			Success:    false,
			TxHash:     txHash,
			Error:      fmt.Errorf("confirmation timeout"),
			Duration:   time.Since(startTime),
		}, fmt.Errorf("confirmation timeout")
	}

	// #region agent log
	debugLogAgent("executor.go:executeStep:afterConfirm", "step confirmed", map[string]interface{}{
		"stepIndex": stepIndex + 1, "confirmed": confirmed,
	}, "H3")
	// #endregion

	// 余额统一通过节点的 GetAvailableBalance() 查询钱包实际链上/交易所余额，不再在此做 mock 更新。

	return &StepResult{
		FromNodeID: fromNode.GetID(),
		ToNodeID:   toNode.GetID(),
		Success:    true,
		TxHash:     txHash,
		Duration:   time.Since(startTime),
	}, nil
}

// calculateAmount 根据 EdgeConfig 计算转账金额
func (p *Pipeline) calculateAmount(node Node, edge *EdgeConfig) (float64, error) {
	// 当边显式配置了 Asset 且为固定金额时，金额单位与边资产一致，可能与节点资产不同，直接返回边金额；余额检查在 executeWithdrawStep 中按边资产完成
	edgeAsset := getEdgeAsset(edge, node)
	if edge.Asset != "" && edge.AmountType == AmountTypeFixed && edgeAsset != node.GetAsset() {
		p.logger.Infof("Node %s: edge.Amount=%.2f %s (edge asset), balance check in executeStep", node.GetID(), edge.Amount, edgeAsset)
		return edge.Amount, nil
	}

	// #region agent log
	debugLogAgent("calculateAmount:entry", "amount calc", map[string]interface{}{
		"pipelineName": p.Name(), "nodeType": string(node.GetType()),
		"nodeID": node.GetID(), "edgeAsset": edgeAsset,
		"amountType": string(edge.AmountType), "edgeAmount": edge.Amount,
	}, "H3")
	// #endregion

	// 当边资产与节点资产不同时（如 backward 用 USDT、节点为 binance-ZAMA），按边资产查余额
	var available float64
	var err error
	if node.GetType() == NodeTypeExchange && edgeAsset != "" && edgeAsset != node.GetAsset() {
		if ex, ok := node.(*ExchangeNode); ok {
			saved := ex.cfg.Asset
			ex.cfg.Asset = edgeAsset
			available, err = node.GetAvailableBalance()
			ex.cfg.Asset = saved
		} else {
			available, err = node.GetAvailableBalance()
		}
	} else {
		available, err = node.GetAvailableBalance()
	}
	if err != nil {
		assetUsed := edgeAsset
		if assetUsed == "" {
			assetUsed = node.GetAsset()
		}
		return 0, fmt.Errorf("get available balance failed (%s): %w", assetUsed, err)
	}

	switch edge.AmountType {
	case AmountTypeFixed:
		if edge.Amount > available {
			// 转账后有损耗时，若可用余额不小于目标的 90%，则按目标的 90% 转出，避免因小额不足而失败
			fallbackAmount := edge.Amount * 0.9
			if available >= fallbackAmount {
				p.logger.Infof("Using 90%% of target amount due to balance shortfall: available=%.4f, target=%.4f, using=%.4f",
					available, edge.Amount, fallbackAmount)
				return fallbackAmount, nil
			}
			return 0, fmt.Errorf("insufficient balance: available=%.4f, required=%.4f", available, edge.Amount)
		}
		return edge.Amount, nil
	case AmountTypePercentage:
		if edge.Amount < 0 || edge.Amount > 1 {
			return 0, fmt.Errorf("invalid percentage: %.4f (must be 0-1)", edge.Amount)
		}
		// 如果配置了 PositionSize，基于仓位大小计算；否则基于可用余额计算
		if edge.PositionSize > 0 {
			calculatedAmount := edge.PositionSize * edge.Amount
			// 不能超过可用余额
			if calculatedAmount > available {
				p.logger.Warnf("Calculated amount (%.4f) exceeds available balance (%.4f), using available balance", calculatedAmount, available)
				return available, nil
			}
			return calculatedAmount, nil
		}
		return available * edge.Amount, nil
	case AmountTypeAll:
		return available, nil
	default:
		return 0, fmt.Errorf("unknown amount type: %s", edge.AmountType)
	}
}

// getEdgeAsset 返回本条边使用的资产：边配置了 Asset 则用边的，否则用节点的
func getEdgeAsset(edge *EdgeConfig, node Node) string {
	if edge != nil && strings.TrimSpace(edge.Asset) != "" {
		return strings.TrimSpace(edge.Asset)
	}
	return node.GetAsset()
}

// needsBridge 判断两个节点之间是否需要跨链（边上的行为：链 A -> 链 B）
func (p *Pipeline) needsBridge(fromNode, toNode Node) bool {
	if fromNode.GetType() != NodeTypeOnchain || toNode.GetType() != NodeTypeOnchain {
		return false
	}
	fromChain := p.getChainID(fromNode)
	toChain := p.getChainID(toNode)
	return fromChain != "" && toChain != "" && fromChain != toChain
}

// executeWithdrawStep 执行提币步骤（从交易所或链上节点提币到目标节点）。
// 交易所→交易所：直接提到目标交易所的充币地址（edge.Network 指定链，如 BEP20/ERC20），不经过中间链上地址，避免多一步链上转账带来的耗时与损耗。
func (p *Pipeline) executeWithdrawStep(fromNode, toNode Node, amount float64, edge *EdgeConfig) (string, error) {
	if !fromNode.CanWithdraw() {
		return "", fmt.Errorf("source node %s does not support withdraw", fromNode.GetID())
	}
	if !toNode.CanDeposit() {
		return "", fmt.Errorf("target node %s does not support deposit", toNode.GetID())
	}

	// 推断提币网络：edge.Network > 目标节点 ChainID > edge.ChainID > ExchangeNode 默认网络
	network := edge.Network
	if network == "" {
		toChainID := p.getChainID(toNode)
		// 如果目标节点没有 chainID，尝试使用边配置的 ChainID
		if toChainID == "" && edge.ChainID != "" {
			toChainID = edge.ChainID
		}
		if toChainID != "" {
			network = chainIDToNetwork(toChainID)
			if network == "" {
				if exchangeNode, ok := toNode.(*ExchangeNode); ok && exchangeNode.cfg.DefaultNetwork != "" {
					network = exchangeNode.cfg.DefaultNetwork
				} else {
					return "", fmt.Errorf("cannot infer network from chainID %s; specify network in edge config to avoid misrouted funds", toChainID)
				}
			} else {
				p.logger.Infof("Inferred network %s from chainID %s", network, toChainID)
			}
		} else {
			if exchangeNode, ok := toNode.(*ExchangeNode); ok && exchangeNode.cfg.DefaultNetwork != "" {
				network = exchangeNode.cfg.DefaultNetwork
			} else {
				return "", fmt.Errorf("cannot determine withdraw network; specify network in edge config to avoid misrouted funds")
			}
		}
	}

	// 本条边使用的资产：边配置优先，否则源节点资产
	actualAsset := getEdgeAsset(edge, fromNode)
	actualAmount := amount
	actualNetwork := network
	var operationDesc string
	needQuoteDeposit := false  // 链上→交易所且使用与节点不同的资产（如 USDT）充值
	needQuoteWithdraw := false // 交易所→链上且使用与节点不同的资产提币

	if fromNode.GetType() == NodeTypeOnchain && toNode.GetType() == NodeTypeExchange {
		operationDesc = "deposit to exchange"
		if actualAsset != fromNode.GetAsset() {
			needQuoteDeposit = true
			p.logger.Infof("Deposit to exchange: amount=%.2f %s (edge asset), network=%s", actualAmount, actualAsset, actualNetwork)
		} else {
			p.logger.Infof("Deposit to exchange: amount=%.8f %s", amount, actualAsset)
		}
	} else if fromNode.GetType() == NodeTypeExchange && actualAsset != fromNode.GetAsset() {
		// 边资产与节点资产不同时，按边资产提币（含 交易所→链上 与 交易所→交易所，保证 pipeline 提现 symbol 统一）
		operationDesc = "withdraw from exchange (edge asset)"
		needQuoteWithdraw = true
		// #region agent log
		debugLogAgent("executeWithdrawStep:exchangeEdgeAsset", "exchange withdraw with edge asset (ex->chain or ex->ex)", map[string]interface{}{
			"amount": amount, "actualAsset": actualAsset, "actualNetwork": actualNetwork,
			"fromNodeID": fromNode.GetID(), "toNodeID": toNode.GetID(), "toNodeType": string(toNode.GetType()),
		}, "H4")
		// #endregion
		p.logger.Infof("Withdraw from exchange: amount=%.2f %s, network=%s", actualAmount, actualAsset, actualNetwork)
	} else {
		actualAmount = amount
		actualNetwork = network
		needQuoteDeposit = false
		if fromNode.GetType() == NodeTypeExchange {
			operationDesc = "withdraw from exchange"
		} else {
			operationDesc = "transfer"
		}
	}

	// 运行时日志：便于排查 51000 等提现错误（确认 edge.Network 与 actualAsset 是否正确传递）
	p.logger.Infow("executeWithdrawStep",
		"edgeNetwork", edge.Network, "actualNetwork", actualNetwork, "actualAsset", actualAsset,
		"fromNodeID", fromNode.GetID(), "toNodeID", toNode.GetID())

	// 提币前检查源交易所提币通道是否开放
	if fromExNode, ok := fromNode.(*ExchangeNode); ok {
		if err := fromExNode.ensureExchange(); err == nil {
			if wnl, ok := fromExNode.ex.(exchange.WithdrawNetworkLister); ok {
				withdrawNets, err := wnl.GetWithdrawNetworks(actualAsset)
				if err == nil {
					withdrawOpen := false
					for _, wn := range withdrawNets {
						if wn.Network == actualNetwork || wn.ChainID == actualNetwork {
							withdrawOpen = true
							break
						}
					}
					if !withdrawOpen && len(withdrawNets) > 0 {
						return "", fmt.Errorf("source exchange %s withdraw channel for %s on network %s is not open; aborting to avoid failure", fromExNode.GetID(), actualAsset, actualNetwork)
					}
				}
			}
		}
	}

	// 提币前检查目标交易所充值通道是否开放，避免提出后目标不入账导致卡币
	if toExNode, ok := toNode.(*ExchangeNode); ok {
		if err := toExNode.ensureExchange(); err == nil {
			if dnl, ok := toExNode.ex.(exchange.DepositNetworkLister); ok {
				depositNets, err := dnl.GetDepositNetworks(actualAsset)
				if err == nil {
					depositOpen := false
					for _, dn := range depositNets {
						if dn.Network == actualNetwork || dn.ChainID == actualNetwork {
							depositOpen = true
							break
						}
					}
					if !depositOpen && len(depositNets) > 0 {
						return "", fmt.Errorf("target exchange %s deposit channel for %s on network %s is not open; aborting withdraw to avoid stuck funds", toExNode.GetID(), actualAsset, actualNetwork)
					}
				}
			}
		}
	}

	// 获取目标节点的充币地址（使用实际资产）
	var depositAddr *model.DepositAddress
	var err error
	// 按本步实际到账资产(symbol)向目标节点查询充币地址，交易所会用该 symbol 调 API
	// #region agent log
	debugLogAgent("executeWithdrawStep:getDepositAddress:entry", "get deposit address by symbol", map[string]interface{}{
		"actualAsset": actualAsset, "actualNetwork": actualNetwork, "toNodeID": toNode.GetID(),
	}, "H-deposit-4018")
	// #endregion
	depositAddr, err = toNode.GetDepositAddress(actualAsset, actualNetwork)
	if err != nil {
		// #region agent log
		debugLogAgent("executeWithdrawStep:getDepositAddress:error", "get deposit address failed", map[string]interface{}{
			"actualAsset": actualAsset, "actualNetwork": actualNetwork, "toNodeID": toNode.GetID(), "err": err.Error(),
		}, "H-deposit-4018")
		// #endregion
		return "", fmt.Errorf("get deposit address failed for asset %s: %w", actualAsset, err)
	}
	// 审计：充提地址来自查询（不写死），仅打前缀便于追溯
	addrPrefix := depositAddr.Address
	if len(addrPrefix) > 8 {
		addrPrefix = addrPrefix[:6] + "..." + addrPrefix[len(addrPrefix)-2:]
	}
	p.logger.Infow("GetDepositAddress (address from query)", "actualAsset", actualAsset, "actualNetwork", actualNetwork, "toNodeID", toNode.GetID(), "addressPrefix", addrPrefix)
	
	// 链上→交易所且边资产与节点不同（如边为 USDT）：用边资产建临时链上节点并转账
	if needQuoteDeposit {
		onchainNode, ok := fromNode.(*OnchainNode)
		if !ok {
			return "", fmt.Errorf("source node is not an OnchainNode")
		}
		chainID := onchainNode.cfg.ChainID
		tokenAddress := p.getTokenAddress(chainID, actualAsset)
		if tokenAddress == "" {
			return "", fmt.Errorf("%s token address not found for chain %s", actualAsset, chainID)
		}
		quoteNode := &OnchainNode{
			cfg: OnchainNodeConfig{
				ID:            onchainNode.cfg.ID + "-" + actualAsset,
				Name:          onchainNode.cfg.Name + " (" + actualAsset + ")",
				ChainID:       chainID,
				AssetSymbol:   actualAsset,
				TokenAddress:  tokenAddress,
				WalletAddress: onchainNode.cfg.WalletAddress,
				Client:        onchainNode.cfg.Client,
			},
		}
		quoteBalance, err := quoteNode.GetAvailableBalance()
		if err != nil {
			return "", fmt.Errorf("failed to check %s balance on chain: %w", actualAsset, err)
		}
		if quoteBalance < actualAmount {
			return "", fmt.Errorf("insufficient %s balance on chain: available=%.2f, required=%.2f", actualAsset, quoteBalance, actualAmount)
		}
		resp, err := quoteNode.Withdraw(actualAmount, depositAddr.Address, actualNetwork, depositAddr.Memo)
		if err != nil {
			return "", fmt.Errorf("%s failed (onchain to exchange, using %s): %w", operationDesc, actualAsset, err)
		}
		return resp.WithdrawID, nil
	}

	// 交易所→链上且边资产与节点不同：临时切换交易所节点资产后提币
	if needQuoteWithdraw {
		exchangeNode, ok := fromNode.(*ExchangeNode)
		if !ok {
			return "", fmt.Errorf("source node is not an ExchangeNode")
		}
		originalAsset := exchangeNode.cfg.Asset
		exchangeNode.cfg.Asset = actualAsset
		quoteBalance, err := exchangeNode.GetAvailableBalance()
		// #region agent log
		debugLogAgent("executeWithdrawStep:quoteWithdraw", "exchange withdraw with edge asset", map[string]interface{}{
			"originalAsset": originalAsset, "actualAsset": actualAsset, "quoteBalance": quoteBalance,
			"actualAmount": actualAmount, "depositAddr": depositAddr.Address, "network": actualNetwork,
		}, "H4")
		// #endregion
		if err != nil {
			exchangeNode.cfg.Asset = originalAsset
			return "", fmt.Errorf("failed to check %s balance on exchange: %w", actualAsset, err)
		}
		if quoteBalance < actualAmount {
			exchangeNode.cfg.Asset = originalAsset
			return "", fmt.Errorf("insufficient %s balance on exchange: available=%.2f, required=%.2f", actualAsset, quoteBalance, actualAmount)
		}
		p.logger.Infof("Withdraw: exchange %s balance=%.2f, withdrawing=%.2f, network=%s, toAddr=%s",
			actualAsset, quoteBalance, actualAmount, actualNetwork, depositAddr.Address)
		resp, err := exchangeNode.Withdraw(actualAmount, depositAddr.Address, actualNetwork, depositAddr.Memo)
		exchangeNode.cfg.Asset = originalAsset
		if err != nil {
			return "", fmt.Errorf("%s failed: %w", operationDesc, err)
		}
		return resp.WithdrawID, nil
	}
	
	resp, err := fromNode.Withdraw(actualAmount, depositAddr.Address, actualNetwork, depositAddr.Memo)
	if err != nil {
		// 根据节点类型提供更准确的错误信息
		if fromNode.GetType() == NodeTypeOnchain && toNode.GetType() == NodeTypeExchange {
			return "", fmt.Errorf("%s failed (onchain to exchange): %w", operationDesc, err)
		}
		return "", fmt.Errorf("withdraw failed: %w", err)
	}

	return resp.WithdrawID, nil
}

// getTokenAddress 获取指定链上某资产的 token 合约地址。
// 查询优先级：Pipeline 已注册的同链 OnchainNode → 常用地址表 → 空串（调用方报错）。
func (p *Pipeline) getTokenAddress(chainID string, symbol string) string {
	if symbol == "" {
		symbol = "USDT"
	}

	// 1) 从 Pipeline 自身节点中查找：同链 OnchainNode 且资产匹配
	for _, n := range p.nodes {
		on, ok := n.(*OnchainNode)
		if !ok {
			continue
		}
		if on.cfg.ChainID == chainID && strings.EqualFold(on.cfg.AssetSymbol, symbol) && on.cfg.TokenAddress != "" {
			return on.cfg.TokenAddress
		}
	}

	// 2) 常用 token 地址表（稳定币 fallback）
	wellKnown := map[string]map[string]string{
		"USDT": {
			"1":     "0xdAC17F958D2ee523a2206206994597C13D831ec7", // Ethereum
			"56":    "0x55d398326f99059fF775485246999027B3197955", // BSC
			"137":   "0xc2132D05D31c914a87C6611C10748AEb04B58e8F", // Polygon
			"42161": "0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9", // Arbitrum
			"10":    "0x94b008aA00579c1307B0EF2c499aD98a8ce58e58", // Optimism
			"8453":  "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", // Base
		},
		"USDC": {
			"1":     "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
			"56":    "0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d",
			"137":   "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359",
			"42161": "0xaf88d065e77c8cC2239327C5EDb3A432268e5831",
			"10":    "0x0b2C639c533813f4Aa9D7837CAf62653d097Ff85",
			"8453":  "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		},
	}
	if addrs, ok := wellKnown[strings.ToUpper(symbol)]; ok {
		if addr, ok2 := addrs[chainID]; ok2 {
			return addr
		}
	}

	return ""
}

// executeBridgeStep 执行跨链步骤（fromNode、toNode 均为链上节点，跨链由边触发）
func (p *Pipeline) executeBridgeStep(fromNode, toNode Node, amount float64, edge *EdgeConfig) (string, string, error) {
	mgr := p.getBridgeManager()
	if mgr == nil {
		return "", "", fmt.Errorf("bridge manager not set: call Pipeline.SetBridgeManager for chain-to-chain edges")
	}

	fromChainID := p.getChainID(fromNode)
	toChainID := p.getChainID(toNode)
	if fromChainID == "" || toChainID == "" {
		return "", "", fmt.Errorf("cannot determine chain IDs for bridge: fromChain=%s, toChain=%s", fromChainID, toChainID)
	}

	toToken := getEdgeAsset(edge, toNode)
	fromToken := getEdgeAsset(edge, fromNode)
	if toToken == "" {
		toToken = toNode.GetAsset()
	}

	depositAddr, err := toNode.GetDepositAddress(toToken, "")
	if err != nil {
		return "", "", fmt.Errorf("get target deposit address failed: %w", err)
	}

	fromDepositAddr, err := fromNode.GetDepositAddress(fromToken, "")
	if err != nil || fromDepositAddr == nil {
		return "", "", fmt.Errorf("cannot get source address for bridge: %w", err)
	}

	protocol := edge.BridgeProtocol
	if protocol == "" {
		protocol = "auto"
	}

	p.logger.Infof("Building bridge request: fromChain=%s, toChain=%s, fromToken=%s, toToken=%s, amount=%.8f, recipient=%s, protocol=%s",
		fromChainID, toChainID, fromToken, toToken, amount, depositAddr.Address, protocol)

	// #region agent log
	debugLogAgent("executeBridgeStep:buildReq", "bridge request", map[string]interface{}{
		"fromChain": fromChainID, "toChain": toChainID, "fromToken": fromToken, "toToken": toToken,
		"amount": amount, "recipient": depositAddr.Address, "protocol": protocol,
	}, "H8")
	// #endregion

	req := &model.BridgeRequest{
		FromChain: fromChainID,
		ToChain:   toChainID,
		FromToken: fromToken,
		ToToken:   toToken,
		Amount:    fmt.Sprintf("%.8f", amount),
		Recipient: depositAddr.Address,
		Protocol:  protocol,
	}

	resp, err := mgr.BridgeToken(req)
	if err != nil {
		return "", "", fmt.Errorf("bridge token failed: %w", err)
	}
	return resp.TxHash, resp.BridgeID, nil
}

// getChainID 从节点获取链ID（辅助函数）；链上节点返回规范化的数字链 ID（如 "1-2" -> "1"），供 needsBridge/executeBridgeStep 与 CCIP 使用。
func (p *Pipeline) getChainID(node Node) string {
	switch n := node.(type) {
	case *OnchainNode:
		return NormalizeChainID(n.cfg.ChainID)
	case *ExchangeNode:
		return ""
	default:
		return ""
	}
}

// chainIDToNetwork 将链ID映射到提币网络名称（用于交易所提币）
// 这个映射基于 Binance 等交易所的网络命名规范
func chainIDToNetwork(chainID string) string {
	networkMap := map[string]string{
		"1":      "ERC20",   // Ethereum
		"56":     "BEP20",   // BSC (Binance Smart Chain)
		"137":    "POLYGON", // Polygon
		"42161":  "ARBITRUM", // Arbitrum One
		"10":     "OPTIMISM", // Optimism
		"43114":  "AVAXC",   // Avalanche C-Chain
		"8453":   "BASE",    // Base
		"250":    "FTM",     // Fantom
		"25":     "CRO",     // Cronos
	}
	if network, ok := networkMap[chainID]; ok {
		return network
	}
	// 如果找不到映射，返回空字符串，让调用方处理
	return ""
}

// protocolFromBridgeID 从 bridgeID 前缀推断协议（ccip-1-56-xxx / lz_xxx / wh_xxx）
func protocolFromBridgeID(bridgeID string) string {
	if len(bridgeID) >= 4 && bridgeID[:4] == "ccip" {
		return "ccip"
	}
	if len(bridgeID) >= 3 && bridgeID[:3] == "lz_" {
		return "layerzero"
	}
	if len(bridgeID) >= 3 && bridgeID[:3] == "wh_" {
		return "wormhole"
	}
	return ""
}

// crossChainBalanceConfirmTolerance 跨链到账确认：目标余额增加量达到预期金额的该比例即视为到账
const crossChainBalanceConfirmTolerance = 0.99

// waitForConfirmation 等待转账确认。expectedAmount 为本次转账金额，跨链时用于「目标余额增加≈金额」即提前确认；非跨链传 0。
func (p *Pipeline) waitForConfirmation(fromNode, toNode Node, txHash string, bridgeID string, edge *EdgeConfig, expectedAmount float64) (bool, error) {
	maxWait := edge.MaxWaitTime
	if maxWait == 0 {
		maxWait = 30 * time.Minute // 默认 30 分钟
	}
	interval := edge.CheckInterval
	if interval == 0 {
		interval = 10 * time.Second // 默认 10 秒
	}
	// CCIP 链上确认往往较快，缩短默认等待与轮询间隔，避免币已到账仍等满 30 分钟
	if edge.BridgeProtocol == "ccip" {
		if maxWait > 10*time.Minute {
			maxWait = 10 * time.Minute
		}
		if interval > 3*time.Second {
			interval = 3 * time.Second
		}
	}

	var balanceBefore float64
	useBalanceCheck := bridgeID != "" && expectedAmount > 0
	if useBalanceCheck {
		if bal, err := toNode.GetAvailableBalance(); err != nil {
			p.logger.Warnf("waitForConfirmation: get toNode balance before failed, skip balance-based early confirm: %v", err)
			useBalanceCheck = false
		} else {
			balanceBefore = bal
		}
	}

	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return false, p.ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return false, fmt.Errorf("confirmation timeout after %v", maxWait)
			}

			var confirmed bool
			var err error

			if bridgeID != "" {
				// 跨链场景：必须使用边指定的协议查状态，禁止用其他协议（避免 CCIP 交易被 LayerZero/Wormhole 误判为 PENDING）
				mgr := p.getBridgeManager()
				if mgr != nil {
					fromChainID := p.getChainID(fromNode)
					toChainID := p.getChainID(toNode)
					protocol := edge.BridgeProtocol
					if protocol == "" || protocol == "auto" {
						protocol = protocolFromBridgeID(bridgeID)
					}
					status, err2 := mgr.GetBridgeStatus(bridgeID, fromChainID, toChainID, protocol)
					if err2 != nil {
						err = err2
					} else if status != nil {
						if status.Status == "FAILED" {
							return false, fmt.Errorf("bridge transaction failed (tx reverted), fromTxHash=%s", status.FromTxHash)
						}
						confirmed = status.Status == "COMPLETED"
					}
				} else {
					err = fmt.Errorf("bridge manager not set")
				}
				// 未通过桥状态确认时，若目标余额增加量接近本次跨链金额，提前视为到账
				if useBalanceCheck && !confirmed && err == nil {
					balanceNow, balErr := toNode.GetAvailableBalance()
					if balErr == nil {
						delta := balanceNow - balanceBefore
						if delta >= expectedAmount*crossChainBalanceConfirmTolerance {
							confirmed = true
							p.logger.Infof("Cross-chain early confirm: toNode balance delta %.4f >= expected %.4f * %.2f", delta, expectedAmount, crossChainBalanceConfirmTolerance)
						}
					}
				}
			} else {
				confirmed, err = fromNode.CheckWithdrawStatus(txHash)
			}

			if err != nil {
				p.logger.Warnf("Check confirmation failed: %v", err)
				continue
			}
			if confirmed {
				// 链上节点且配置了确认数要求：额外检查区块确认数
				if edge.Confirmations > 0 {
					ok, confErr := p.checkOnchainConfirmations(fromNode, txHash, edge.Confirmations)
					if confErr != nil {
						p.logger.Warnf("Confirmations check error: %v", confErr)
						continue
					}
					if !ok {
						continue
					}
				}
				return true, nil
			}
		}
	}
}

// checkOnchainConfirmations 检查链上交易是否达到所需确认数。
// 仅对 OnchainNode 有效，ExchangeNode 的确认数由交易所自身管理。
func (p *Pipeline) checkOnchainConfirmations(node Node, txHash string, required int) (bool, error) {
	on, ok := node.(*OnchainNode)
	if !ok {
		return true, nil // 非链上节点不需要额外确认
	}
	client, err := on.getRPCClient()
	if err != nil {
		return false, err
	}
	defer client.Close()

	receipt, err := client.TransactionReceipt(context.Background(), common.HexToHash(txHash))
	if err != nil || receipt == nil {
		return false, nil
	}
	currentBlock, err := client.BlockNumber(context.Background())
	if err != nil {
		return false, err
	}
	confirmations := int(currentBlock - receipt.BlockNumber.Uint64())
	if confirmations < required {
		p.logger.Debugf("Waiting for confirmations: %d/%d (txHash=%s)", confirmations, required, txHash)
		return false, nil
	}
	return true, nil
}

// Stop 停止 Pipeline 执行
func (p *Pipeline) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
	}
	if p.status == PipelineStatusRunning {
		p.status = PipelineStatusPaused
	}
}
