package detector

import (
	"context"
	"fmt"
	"strings"

	"github.com/qw225967/auto-monitor/internal/detector/registry"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/onchain/bridge"
	"github.com/qw225967/auto-monitor/internal/tokenregistry"
)

// ArbitrageAdapter 适配 auto-arbitrage 的 pipeline 路由探测
var _ Detector = (*ArbitrageAdapter)(nil)
var _ RegistryRefresher = (*ArbitrageAdapter)(nil)

type ArbitrageAdapter struct {
	bridgeMgr *bridge.Manager
	reg       registry.NetworkRegistry
	builder   *PipelineBuilder
}

// RefreshNetworks 实现 RegistryRefresher，用价差 symbol 刷新充提网络（每 30s 探测前调用）
func (a *ArbitrageAdapter) RefreshNetworks(ctx context.Context, symbols []string) {
	if apiReg, ok := a.reg.(*registry.APINetworkRegistry); ok {
		apiReg.Refresh(ctx, symbols)
	}
}

// NewArbitrageAdapter 创建适配器，bridgeMgr 可为 nil（跨链段将标记为不可用）
// 使用 APINetworkRegistry 从交易所公开 API 实时获取充提网络，不再依赖静态配置
func NewArbitrageAdapter(bridgeMgr *bridge.Manager) *ArbitrageAdapter {
	reg := registry.NewAPINetworkRegistry()
	return &ArbitrageAdapter{
		bridgeMgr: bridgeMgr,
		reg:       reg,
		builder:   NewPipelineBuilder(reg),
	}
}

// exchangeToNodeID 交易所名转节点 ID（SeeingStone 用大写如 BITGET，pipeline 用小写如 bitget）
func exchangeToNodeID(ex string) string {
	s := strings.ToLower(strings.TrimSpace(ex))
	if s == "okx" {
		return "okex" // pipeline 使用 okex
	}
	return s
}

// nodeIDFromDisplay 将 "BITGET" 或 "Chain_56" 转为节点 ID，不支持则返回空
func nodeIDFromDisplay(s string) string {
	if cid, ok := chainFromDisplay(s); ok {
		return "onchain:" + cid
	}
	node := exchangeToNodeID(s)
	if supportedExchanges[node] {
		return node
	}
	return ""
}

// 常用链节点
var commonChains = []string{"onchain:56", "onchain:1", "onchain:195"} // BSC, ETH, TRON

// 支持的交易所节点（SeeingStone 可能用 OKX，pipeline 用 okex）
var supportedExchanges = map[string]bool{
	"binance": true, "bybit": true, "bitget": true, "gate": true, "okex": true, "okx": true,
	"hyperliquid": true, "lighter": true, "aster": true,
}

// DetectRoutes 探测从 buyExchange 到 sellExchange 的物理通路
// 支持交易所到链：buyExchange/sellExchange 可为 "BITGET" 或 "Chain_56"
func (a *ArbitrageAdapter) DetectRoutes(ctx context.Context, symbol, buyExchange, sellExchange string) ([]model.PhysicalPath, error) {
	_ = ctx
	buyNode := nodeIDFromDisplay(buyExchange)
	sellNode := nodeIDFromDisplay(sellExchange)
	if buyNode == "" {
		return nil, fmt.Errorf("unsupported buy: %s", buyExchange)
	}
	if sellNode == "" {
		return nil, fmt.Errorf("unsupported sell: %s", sellExchange)
	}

	// 提取资产符号（如 ETHUSDT -> ETH，用于充提网络查询）
	asset, _ := tokenregistry.SymbolToAsset(symbol)
	if asset == "" {
		asset = "USDT"
	}

	// 使用 NetworkRegistry + PipelineBuilder 生成路径
	paths, err := a.builder.BuildPaths(asset, buyExchange, sellExchange)
	if err != nil || len(paths) == 0 {
		// 回退：简单直连 + 经常见链
		paths = [][]string{{buyNode, sellNode}}
		for _, chain := range commonChains {
			paths = append(paths, []string{buyNode, chain, sellNode})
		}
	}

	// 3. 依次对每条路径做可达性探测，4. 只输出可达路线
	var result []model.PhysicalPath
	for i, path := range paths {
		req := &model.RouteProbeRequest{
			Symbol:      asset,
			Path:        path,
			ProbeAmount: "100",
		}
		probeResult, err := routeProbe(req, a.bridgeMgr, a.reg)
		if err != nil {
			continue
		}
		phys := a.convertToPhysicalPath(probeResult, i+1)
		// 只输出可达路线：所有段 Available 才加入
		if len(phys.Hops) > 0 && phys.OverallStatus == "ok" {
			result = append(result, phys)
		}
	}

	if len(result) == 0 {
		// 无可达路径时回退 Mock，供前端展示占位
		return NewMock().DetectRoutes(ctx, symbol, buyExchange, sellExchange)
	}
	return result, nil
}

// convertToPhysicalPath 将 RouteProbeResult 转为 PhysicalPath
func (a *ArbitrageAdapter) convertToPhysicalPath(r *model.RouteProbeResult, idx int) model.PhysicalPath {
	pathID := fmt.Sprintf("Path_%02d", idx)
	status := "ok"
	for _, seg := range r.Segments {
		if !seg.Available {
			status = "maintenance"
			break
		}
	}

	var hops []model.Hop
	for _, seg := range r.Segments {
		edgeDesc := edgeDescFromSegment(seg)
		hops = append(hops, model.Hop{
			FromNode: nodeIDToDisplay(seg.FromNodeID),
			EdgeDesc: edgeDesc,
			ToNode:   nodeIDToDisplay(seg.ToNodeID),
			Status:   mapBoolToStatus(seg.Available),
		})
	}

	return model.PhysicalPath{
		PathID:        pathID,
		Hops:          hops,
		OverallStatus: status,
	}
}

func chainName(chainID string) string {
	switch chainID {
	case "56":
		return "BSC"
	case "1":
		return "ETH"
	case "195":
		return "TRON"
	case "137":
		return "Polygon"
	default:
		return chainID
	}
}

func edgeDescFromSegment(seg model.SegmentProbeResult) string {
	if seg.EdgeLabel != "" {
		return seg.EdgeLabel
	}
	if seg.BridgeProtocol != "" {
		return "跨链" + seg.BridgeProtocol
	}
	if seg.WithdrawNetworkChainID != "" {
		return "提现" + chainName(seg.WithdrawNetworkChainID)
	}
	if seg.Type == model.SegmentTypeDeposit {
		return "充值" + chainName(chainFromNodeID(seg.FromNodeID))
	}
	if seg.Type == model.SegmentTypeWithdraw {
		return "提现" + chainName(chainFromNodeID(seg.ToNodeID))
	}
	if seg.Type == model.SegmentTypeExchangeToExchange {
		return "交易所直转"
	}
	return seg.Type
}

func chainFromNodeID(nodeID string) string {
	if strings.HasPrefix(nodeID, "onchain:") {
		return strings.TrimPrefix(nodeID, "onchain:")
	}
	return ""
}

func nodeIDToDisplay(nodeID string) string {
	if strings.HasPrefix(nodeID, "onchain:") {
		return chainName(strings.TrimPrefix(nodeID, "onchain:")) + "链"
	}
	return strings.ToUpper(nodeID)
}

func mapBoolToStatus(available bool) string {
	if available {
		return "ok"
	}
	return "maintenance"
}
