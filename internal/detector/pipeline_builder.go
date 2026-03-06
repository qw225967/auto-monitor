package detector

import (
	"sort"
	"strings"

	"github.com/qw225967/auto-monitor/internal/detector/registry"
	"github.com/qw225967/auto-monitor/internal/model"
)

const (
	maxRouteHops  = 4
	onchainPrefix = "onchain:"
	chainPrefix   = "chain_"
)

// chainFromDisplay 解析 "Chain_56" -> "56"
func chainFromDisplay(s string) (chainID string, ok bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if strings.HasPrefix(s, chainPrefix) {
		return strings.TrimPrefix(s, chainPrefix), true
	}
	return "", false
}

// PipelineBuilder 根据 NetworkRegistry 构建邻接图并枚举路径
type PipelineBuilder struct {
	reg registry.NetworkRegistry
}

// NewPipelineBuilder 创建 pipeline 构建器
func NewPipelineBuilder(reg registry.NetworkRegistry) *PipelineBuilder {
	return &PipelineBuilder{reg: reg}
}

// BuildPaths 从 buyExchange 到 sellExchange 枚举所有可达路径（基于充提网络）
// 支持交易所或链：buyExchange/sellExchange 可为 "BITGET" 或 "Chain_56"
// 返回路径列表，每条路径为节点 ID 序列，如 ["binance","onchain:56","bitget"]
func (b *PipelineBuilder) BuildPaths(asset, buyExchange, sellExchange string) ([][]string, error) {
	var buy, sell string
	var buyChains, sellChains map[string]bool

	if cid, ok := chainFromDisplay(buyExchange); ok {
		buy = onchainPrefix + cid
		buyChains = map[string]bool{cid: true}
	} else {
		buy = exchangeToNodeID(buyExchange)
		wd, _ := b.reg.GetWithdrawNetworks(buy, asset)
		buyChains = chainIDsFromNetworks(wd)
	}

	if cid, ok := chainFromDisplay(sellExchange); ok {
		sell = onchainPrefix + cid
		sellChains = map[string]bool{cid: true}
	} else {
		sell = exchangeToNodeID(sellExchange)
		dep, _ := b.reg.GetDepositNetworks(sell, asset)
		sellChains = chainIDsFromNetworks(dep)
	}

	adj := b.buildAdjacency(buy, sell, buyChains, sellChains)
	paths := findAllRoutes(buy, sell, adj, maxRouteHops)

	// 优先展示直连/短路径（如 Bitget 直提 ETH），避免绕道 BSC 的路径排前面
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) < len(paths[j]) })

	return paths, nil
}

// findAllRoutes 在图上从 source 到 dest 枚举所有路径（自 pipeline 复制，避免依赖 pipeline 包）
func findAllRoutes(source, dest string, adj map[string][]string, maxHops int) [][]string {
	if maxHops < 1 {
		maxHops = 1
	}
	if source == dest {
		return [][]string{{source}}
	}
	var out [][]string
	visited := make(map[string]bool)
	path := make([]string, 0, maxHops+1)

	var dfs func(cur string, hops int)
	dfs = func(cur string, hops int) {
		if hops > maxHops {
			return
		}
		path = append(path, cur)
		defer func() { path = path[:len(path)-1] }()

		if cur == dest {
			pc := make([]string, len(path))
			copy(pc, path)
			out = append(out, pc)
			return
		}
		for _, next := range adj[cur] {
			if visited[next] {
				continue
			}
			visited[next] = true
			dfs(next, hops+1)
			visited[next] = false
		}
	}

	visited[source] = true
	dfs(source, 0)
	visited[source] = false
	return out
}

func chainIDsFromNetworks(nets []model.WithdrawNetworkInfo) map[string]bool {
	m := make(map[string]bool)
	for _, n := range nets {
		cid := strings.TrimSpace(n.ChainID)
		if cid != "" && n.WithdrawEnable {
			m[cid] = true
		}
	}
	return m
}

// buildAdjacency 构建邻接表
// 节点：交易所（小写）、链（onchain:chainID）
// 边：交易所->链（可提现）、链->交易所（可充值）、链->链（跨链桥）、交易所->交易所（直连）
func (b *PipelineBuilder) buildAdjacency(buy, sell string, buyChains, sellChains map[string]bool) map[string][]string {
	adj := make(map[string][]string)

	// 买交易所 -> 卖交易所（直连，交易所间转账）
	adj[buy] = append(adj[buy], sell)

	// 买交易所 -> 可提现的链
	for cid := range buyChains {
		nodeID := onchainPrefix + cid
		adj[buy] = append(adj[buy], nodeID)
	}

	// 链 -> 卖交易所（若卖方可充值该链）；或 链 -> 目标链（跨链）
	for cid := range sellChains {
		nodeID := onchainPrefix + cid
		if nodeID == sell {
			continue // 避免自环
		}
		adj[nodeID] = append(adj[nodeID], sell)
	}

	// 链 -> 链（跨链桥，常见链之间）
	allChains := make(map[string]bool)
	for c := range buyChains {
		allChains[c] = true
	}
	for c := range sellChains {
		allChains[c] = true
	}
	commonChains := []string{"1", "56", "195", "137", "42161", "10", "8453", "43114"}
	for _, c1 := range commonChains {
		if !allChains[c1] {
			continue
		}
		n1 := onchainPrefix + c1
		for _, c2 := range commonChains {
			if c1 == c2 {
				continue
			}
			if !allChains[c2] {
				continue
			}
			adj[n1] = append(adj[n1], onchainPrefix+c2)
		}
	}

	return adj
}
