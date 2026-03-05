package pipeline

import (
	"strings"
	"sync"
	"time"

	"auto-arbitrage/internal/utils/logger"

	"go.uber.org/zap"
)

// AutoWithdrawEnabledForBrickPipeline 由 web 层注册：查询某 symbol+triggerId 是否开启自动充提。未注册时视为全部启用。
// 关闭时余额转账（中间节点 RunStep）将不触发。
var AutoWithdrawEnabledForBrickPipeline func(symbol, triggerIDStr string) bool

// RegisterAutoWithdrawChecker 注册自动充提开关查询函数；关闭时余额转账（中间节点 RunStep）将不触发。
func RegisterAutoWithdrawChecker(fn func(symbol, triggerIDStr string) bool) {
	AutoWithdrawEnabledForBrickPipeline = fn
}

// symbolAndTriggerIDFromBrickPipelineName 从搬砖 pipeline 名称解析 symbol 与 triggerId。
// 例："brick-POWERUSDT-forward" -> ("POWERUSDT",""); "brick-POWERUSDT-918-backward" -> ("POWERUSDT","918")。
func symbolAndTriggerIDFromBrickPipelineName(name string) (symbol, triggerIDStr string) {
	if name == "" || !strings.HasPrefix(name, "brick-") {
		return "", ""
	}
	rest := name[len("brick-"):]
	if strings.HasSuffix(rest, "-forward") {
		rest = rest[:len(rest)-len("-forward")]
	} else if strings.HasSuffix(rest, "-backward") {
		rest = rest[:len(rest)-len("-backward")]
	} else {
		return "", ""
	}
	if rest == "" {
		return "", ""
	}
	parts := strings.SplitN(rest, "-", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

// MiddleNodeScheduler 中间节点定时调度器：按固定间隔检测各 Pipeline 中间节点余额，
// 有余额则触发「余额转账」流程（RunStep），与正常流程分开展示。
// 多个 pipeline 的余额转账并行触发（各起 goroutine），与正常流程一致：pipeline 之间并行、节点之间串行。
// 仅当该 pipeline 对应 symbol 的自动充提开关开启时才会触发余额转账。
type MiddleNodeScheduler struct {
	mu         sync.Mutex
	interval   time.Duration
	stopCh     chan struct{}
	stopped    bool
	logger     *zap.SugaredLogger
	triggerCh  chan struct{} // 用于立即触发一次检测（pipeline 应用后）
}

var (
	globalMiddleNodeScheduler *MiddleNodeScheduler
	schedulerOnce             sync.Once
)

// GetMiddleNodeScheduler 返回全局中间节点调度器（单例）。
func GetMiddleNodeScheduler() *MiddleNodeScheduler {
	schedulerOnce.Do(func() {
		globalMiddleNodeScheduler = &MiddleNodeScheduler{
			interval:  15 * time.Second, // 15 秒检测一次，提高余额转账触发频次
			stopCh:   make(chan struct{}),
			triggerCh: make(chan struct{}, 4), // 缓冲，避免阻塞调用方
			logger:   logger.GetLoggerInstance().Named("Pipeline.MiddleNodeScheduler").Sugar(),
		}
	})
	return globalMiddleNodeScheduler
}

// SetInterval 设置检测间隔（仅在 Start 前生效）。
func (s *MiddleNodeScheduler) SetInterval(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d > 0 {
		s.interval = d
	}
}

// TriggerImmediateCheck 立即触发一次余额检测（非阻塞）。用于 pipeline 应用后实时检测。
func (s *MiddleNodeScheduler) TriggerImmediateCheck() {
	s.mu.Lock()
	ch := s.triggerCh
	s.mu.Unlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
			// 已有待处理触发，跳过
		}
	}
}

// Start 启动调度器：每隔 interval 扫描所有 Pipeline 的中间节点，有余额则执行余额转账（RunStep）。
// 支持 TriggerImmediateCheck 立即触发检测。应在单独 goroutine 中调用：go GetMiddleNodeScheduler().Start()
func (s *MiddleNodeScheduler) Start() {
	s.mu.Lock()
	if s.stopped {
		s.stopCh = make(chan struct{})
		s.triggerCh = make(chan struct{}, 4)
		s.stopped = false
	}
	interval := s.interval
	triggerCh := s.triggerCh
	s.mu.Unlock()

	s.logger.Infof("MiddleNodeScheduler started (balance transfer flow), check interval=%v", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// 启动时立即执行一次检测
	s.tick()

	for {
		select {
		case <-s.stopCh:
			s.logger.Info("MiddleNodeScheduler stopped")
			return
		case <-triggerCh:
			s.tick()
		case <-ticker.C:
			s.tick()
		}
	}
}

// Stop 停止调度器。
func (s *MiddleNodeScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.stopped = true
		close(s.stopCh)
		s.triggerCh = nil // 避免 Stop 后仍写入
	}
}

func (s *MiddleNodeScheduler) tick() {
	pm := GetPipelineManager()
	pipelines := pm.ListPipelines()
	if len(pipelines) == 0 {
		s.logger.Infof("MiddleNodeScheduler tick: no pipelines in manager (balance transfer needs 3+ node pipeline, e.g. A→B→C)")
		return
	}

	hadCandidates := false
	for _, p := range pipelines {
		name := p.Name()
		// 主 pipeline 正在 Run 时跳过：Run 已负责中间 step（RunWhenReady），若同时触发 RunStep 会竞态，
		// 导致 Run 在 RunWhenReady 中永远等不到余额（被 RunStep 抢先转走），status 卡 running 约 30 分钟
		if p.Status() == PipelineStatusRunning {
			s.logger.Debugf("MiddleNodeScheduler: pipeline %s skipped: main pipeline running", name)
			continue
		}
		if p.GetBalanceTransferStatus() == "running" {
			s.logger.Debugf("MiddleNodeScheduler: pipeline %s skipped: balance transfer already running", name)
			continue
		}
		symbol, triggerIDStr := symbolAndTriggerIDFromBrickPipelineName(name)
		// 仅对 brick pipeline 做自动充提检查；非 brick pipeline（symbol 为空）不触发余额转账，避免无 trigger 关联的 pipeline 误触发
		if symbol == "" {
			s.logger.Debugf("MiddleNodeScheduler: pipeline %s skipped: not a brick pipeline (no symbol parsed)", name)
			continue
		}
		if fn := AutoWithdrawEnabledForBrickPipeline; fn != nil {
			enabled := fn(symbol, triggerIDStr)
			if !enabled {
				s.logger.Infof("MiddleNodeScheduler: pipeline %s (symbol=%s, triggerId=%s) skipped: auto-withdraw disabled", name, symbol, triggerIDStr)
				continue
			}
			s.logger.Infof("MiddleNodeScheduler: pipeline %s (symbol=%s, triggerId=%s) auto-withdraw enabled, checking middle-node balance", name, symbol, triggerIDStr)
		}
		// fn == nil 时（未注册）视为全部启用，保持向后兼容
		nodes := p.Nodes()
		if len(nodes) < 3 {
			s.logger.Debugf("MiddleNodeScheduler: pipeline %s skipped: nodes=%d (need 3+ for middle transfer)", name, len(nodes))
			continue
		}
		hadCandidates = true
		triggered := false
		for stepIndex := 1; stepIndex <= len(nodes)-2; stepIndex++ {
			fromNode := nodes[stepIndex]
			toNode := nodes[stepIndex+1]
			edge, hasEdge := p.GetEdgeConfig(fromNode.GetID(), toNode.GetID())
			if !hasEdge || edge == nil {
				edge = &EdgeConfig{
					AmountType:    AmountTypeAll,
					MaxWaitTime:   30 * time.Minute,
					CheckInterval: 10 * time.Second,
				}
			}
			// 中间节点余额转账：有余额就转（RunStep 内会 forceAllBalance 全转），判断时按「有余额即可」不要求达到首步固定金额
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
					checkEdge.CheckInterval = 10 * time.Second
				}
			}
			amount, err := p.calculateAmount(fromNode, checkEdge)
			if err != nil || amount <= 0 {
				s.logger.Infof("MiddleNodeScheduler: pipeline %s middle step %d (%s -> %s) no balance: amount=%.4f err=%v",
					name, stepIndex+1, fromNode.GetName(), toNode.GetName(), amount, err)
				continue
			}
			// 并行：每个 pipeline 的余额转账在独立 goroutine 中执行，pipeline 之间互不阻塞
			pipeline := p
			idx := stepIndex
			amt := amount
			go func() {
				s.logger.Infof("Pipeline %s: middle node step %d has balance %.4f, triggering balance transfer RunStep(%d)", pipeline.Name(), idx+1, amt, idx)
				if err := pipeline.RunStep(idx); err != nil {
					s.logger.Warnf("Pipeline %s RunStep(%d) failed: %v", pipeline.Name(), idx, err)
				}
			}()
			triggered = true
			break
		}
		if !triggered && hadCandidates {
			s.logger.Debugf("MiddleNodeScheduler: pipeline %s has 3+ nodes but no middle step with balance", name)
		}
	}
	if len(pipelines) > 0 && !hadCandidates {
		s.logger.Infof("MiddleNodeScheduler tick: %d pipeline(s) but none with 3+ nodes (balance transfer only for A→B→C style)", len(pipelines))
	}
}
