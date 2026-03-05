package bridge

import (
	"fmt"
	"strconv"
	"sync"

	"auto-arbitrage/internal/model"
)

// Manager 跨链协议管理器
type Manager struct {
	mu        sync.RWMutex
	protocols map[string]BridgeProtocol
	autoSelect bool
}

// NewManager 创建跨链协议管理器
func NewManager(autoSelect bool) *Manager {
	return &Manager{
		protocols:  make(map[string]BridgeProtocol),
		autoSelect: autoSelect,
	}
}

// RegisterProtocol 注册跨链协议
func (m *Manager) RegisterProtocol(protocol BridgeProtocol) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.protocols[protocol.GetName()] = protocol
}

// GetProtocol 获取指定协议
func (m *Manager) GetProtocol(name string) (BridgeProtocol, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	protocol, ok := m.protocols[name]
	if !ok {
		return nil, fmt.Errorf("protocol %s not found", name)
	}
	return protocol, nil
}

// BridgeToken 执行跨链转账（自动选择最优协议或使用指定协议）
func (m *Manager) BridgeToken(req *model.BridgeRequest) (*model.BridgeResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 如果指定了协议，直接使用
	if req.Protocol != "" && req.Protocol != "auto" {
		protocol, ok := m.protocols[req.Protocol]
		if !ok {
			return nil, fmt.Errorf("protocol %s not found", req.Protocol)
		}
		return protocol.BridgeToken(req)
	}

	// 自动选择最优协议
	if m.autoSelect {
		protocol, err := m.selectBestProtocol(req)
		if err != nil {
			return nil, err
		}
		return protocol.BridgeToken(req)
	}

	// 如果不自动选择，尝试所有就绪的协议
	for _, protocol := range m.protocols {
		if protocol.IsChainPairSupported(req.FromChain, req.ToChain) {
			if err := protocol.CheckBridgeReady(req.FromChain, req.ToChain, req.FromToken); err != nil {
				continue
			}
			return protocol.BridgeToken(req)
		}
	}

	return nil, fmt.Errorf("no supported protocol for chain pair %s -> %s", req.FromChain, req.ToChain)
}

// GetBridgeStatus 查询跨链状态
func (m *Manager) GetBridgeStatus(txHash string, fromChain, toChain string, protocolName string) (*model.BridgeStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 如果指定了协议，直接使用
	if protocolName != "" {
		protocol, ok := m.protocols[protocolName]
		if !ok {
			return nil, fmt.Errorf("protocol %s not found", protocolName)
		}
		return protocol.GetBridgeStatus(txHash, fromChain, toChain)
	}

	// 尝试所有协议
	for _, protocol := range m.protocols {
		if protocol.IsChainPairSupported(fromChain, toChain) {
			status, err := protocol.GetBridgeStatus(txHash, fromChain, toChain)
			if err == nil && status != nil {
				return status, nil
			}
		}
	}

	return nil, fmt.Errorf("bridge status not found for txHash %s", txHash)
}

// CheckBridgeReady 预检查跨链条件是否满足。
// 根据 protocol 指定协议（"layerzero"/"wormhole"/"ccip"），若为空或 "auto" 则检查所有支持该链对的协议，
// 只要有一个协议就绪即返回 nil；全部不就绪则返回汇总错误。
func (m *Manager) CheckBridgeReady(fromChain, toChain, tokenSymbol, protocol string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 指定了具体协议
	if protocol != "" && protocol != "auto" {
		p, ok := m.protocols[protocol]
		if !ok {
			return fmt.Errorf("protocol %s not found", protocol)
		}
		return p.CheckBridgeReady(fromChain, toChain, tokenSymbol)
	}

	// 自动模式：只要有一个支持的协议就绪即可
	var errors []string
	for name, p := range m.protocols {
		if !p.IsChainPairSupported(fromChain, toChain) {
			continue
		}
		if err := p.CheckBridgeReady(fromChain, toChain, tokenSymbol); err != nil {
			errors = append(errors, fmt.Sprintf("[%s] %v", name, err))
		} else {
			return nil // 至少一个协议就绪
		}
	}
	if len(errors) == 0 {
		return fmt.Errorf("no supported protocol for chain pair %s -> %s (token: %s)", fromChain, toChain, tokenSymbol)
	}
	return fmt.Errorf("no bridge protocol ready for %s -> %s (token: %s): %s",
		fromChain, toChain, tokenSymbol, joinErrors(errors))
}

// joinErrors 将多个错误字符串用分号连接
func joinErrors(errs []string) string {
	result := ""
	for i, e := range errs {
		if i > 0 {
			result += "; "
		}
		result += e
	}
	return result
}

// GetBridgeQuote 获取跨链报价（返回所有协议的报价）
// 仅包含 CheckBridgeReady 通过的协议，避免展示不可达的跨链路由（如 POWER 在 LayerZero 无真实 OFT 仍被 DiscoverToken 误注册）
func (m *Manager) GetBridgeQuote(req *model.BridgeQuoteRequest) (*model.BridgeQuote, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tokenSymbol := req.FromToken
	if tokenSymbol == "" {
		tokenSymbol = req.ToToken
	}
	if tokenSymbol == "" {
		tokenSymbol = "USDT"
	}

	quotes := make([]model.ProtocolQuote, 0)
	for _, protocol := range m.protocols {
		if !protocol.IsChainPairSupported(req.FromChain, req.ToChain) {
			continue
		}
		if err := protocol.CheckBridgeReady(req.FromChain, req.ToChain, tokenSymbol); err != nil {
			continue // 协议未就绪（如 token 未配置、OFT 不存在），不加入报价
		}
		quote, err := protocol.GetQuote(req)
		if err == nil && quote != nil && quote.Supported {
			quotes = append(quotes, *quote)
		}
	}

	return &model.BridgeQuote{
		Protocols: quotes,
	}, nil
}

// selectBestProtocol 选择最优协议（根据费用、速度、支持情况）
func (m *Manager) selectBestProtocol(req *model.BridgeRequest) (BridgeProtocol, error) {
	quoteReq := &model.BridgeQuoteRequest{
		FromChain: req.FromChain,
		ToChain:   req.ToChain,
		FromToken:  req.FromToken,
		ToToken:    req.ToToken,
		Amount:     req.Amount,
	}

	type candidate struct {
		protocol BridgeProtocol
		score    float64
		fee      string
		time     int64
	}

	var candidates []candidate
	for _, protocol := range m.protocols {
		if !protocol.IsChainPairSupported(req.FromChain, req.ToChain) {
			continue
		}
		// 跳过未就绪的协议（如 CCIP BridgeToken 尚未实现）
		if err := protocol.CheckBridgeReady(req.FromChain, req.ToChain, req.FromToken); err != nil {
			continue
		}
		quote, err := protocol.GetQuote(quoteReq)
		if err != nil || quote == nil || !quote.Supported {
			continue
		}
		score := m.calculateProtocolScore(quote)
		candidates = append(candidates, candidate{
			protocol: protocol,
			score:    score,
			fee:      quote.Fee,
			time:     quote.EstimatedTime,
		})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no supported protocol for chain pair %s -> %s", req.FromChain, req.ToChain)
	}

	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.score > best.score {
			best = c
		}
	}

	return best.protocol, nil
}

// calculateProtocolScore 计算协议评分（费用越低、时间越短，分数越高）
// 权重分配：费用 60%、时间 25%、可靠性(MinAmount 可用性)15%
func (m *Manager) calculateProtocolScore(quote *model.ProtocolQuote) float64 {
	if !quote.Supported {
		return -1
	}

	fee, err := strconv.ParseFloat(quote.Fee, 64)
	if err != nil || fee < 0 {
		fee = 100 // 无法解析时给予惩罚分
	}
	// 费用评分：$0 = 1.0, $50+ = 0.0, 线性衰减
	feeScore := 1.0 - fee/50.0
	if feeScore < 0 {
		feeScore = 0
	}

	// 时间评分：0s = 1.0, 1800s(30min)+ = 0.0
	timeScore := 1.0 - float64(quote.EstimatedTime)/1800.0
	if timeScore < 0 {
		timeScore = 0
	}

	// 可靠性评分：MinAmount 可解析且 > 0 说明协议返回了真实报价
	reliabilityScore := 0.5
	if quote.MinAmount != "" {
		if minAmt, parseErr := strconv.ParseFloat(quote.MinAmount, 64); parseErr == nil && minAmt > 0 {
			reliabilityScore = 1.0
		}
	}

	return 0.60*feeScore + 0.25*timeScore + 0.15*reliabilityScore
}
