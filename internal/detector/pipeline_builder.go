package detector

import (
	"log"
	"sort"
	"strings"

	"github.com/qw225967/auto-monitor/internal/detector/registry"
	"github.com/qw225967/auto-monitor/internal/model"
)

const (
	maxRouteHops  = 3 // 3 跳 = 4 节点，如 exchange->chain1->chain2->exchange
	onchainPrefix = "onchain:"
	chainPrefix   = "chain_"
)

// chainPreference 链展示优先级（ETH 优先于 BSC），同长度路径时优先展示
var chainPreference = map[string]int{
	"1": 0, "56": 1, "195": 2, "137": 3, "42161": 4, "10": 5, "8453": 6, "43114": 7,
}

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
		if len(buyChains) == 0 {
			log.Printf("[BuildPaths] %s %s 提现链为空: buy=%s", buyExchange, asset, buy)
		}
	}

	if cid, ok := chainFromDisplay(sellExchange); ok {
		sell = onchainPrefix + cid
		sellChains = map[string]bool{cid: true}
	} else {
		sell = exchangeToNodeID(sellExchange)
		dep, _ := b.reg.GetDepositNetworks(sell, asset)
		sellChains = chainIDsFromNetworks(dep)
		if len(sellChains) == 0 {
			log.Printf("[BuildPaths] %s %s 充值链为空: sell=%s", sellExchange, asset, sell)
		}
	}

	adj := b.buildAdjacency(buy, sell, buyChains, sellChains)
	paths := findAllRoutes(buy, sell, adj, maxRouteHops)
	if len(paths) == 0 && len(buyChains) > 0 && len(sellChains) > 0 {
		log.Printf("[BuildPaths] %s->%s %s 有链但无路径: buyChains=%v sellChains=%v", buyExchange, sellExchange, asset, mapKeys(buyChains), mapKeys(sellChains))
	}

	// 1) 优先短路径  2) 同长度时优先 ETH 等常用链（Bitget 支持 ETH 时不应显示 BSC）
	sort.Slice(paths, func(i, j int) bool {
		pi, pj := paths[i], paths[j]
		if len(pi) != len(pj) {
			return len(pi) < len(pj)
		}
		return pathChainScore(pi) < pathChainScore(pj)
	})

	return paths, nil
}

// pathChainScore 路径中首个链的优先级分（越小越优先展示）
func pathChainScore(path []string) int {
	for _, node := range path {
		if strings.HasPrefix(node, onchainPrefix) {
			cid := strings.TrimPrefix(node, onchainPrefix)
			if p, ok := chainPreference[cid]; ok {
				return p
			}
			return 999
		}
	}
	return 999
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

func sortedChainsByPreference(chains map[string]bool) []string {
	var out []string
	for cid := range chains {
		out = append(out, cid)
	}
	sort.Slice(out, func(i, j int) bool {
		pi, oki := chainPreference[out[i]]
		pj, okj := chainPreference[out[j]]
		if !oki {
			pi = 999
		}
		if !okj {
			pj = 999
		}
		return pi < pj
	})
	return out
}

func mapKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
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
// 边：交易所->链（可提现）、链->交易所（可充值）、链->链（跨链桥）
// 不添加交易所->交易所直连：CEX 间无法直接转账，必须经链提现+充值
func (b *PipelineBuilder) buildAdjacency(buy, sell string, buyChains, sellChains map[string]bool) map[string][]string {
	adj := make(map[string][]string)

	// 买交易所 -> 可提现的链（按链优先级排序，ETH 优先）
	// 跳过自环：当 buy 已是链节点（如 onchain:1）时，不再添加 buy->buy
	buyChainList := sortedChainsByPreference(buyChains)
	for _, cid := range buyChainList {
		next := onchainPrefix + cid
		if next == buy {
			continue
		}
		adj[buy] = append(adj[buy], next)
	}

	// 链 -> 卖交易所（若卖方可充值该链）；或 链 -> 目标链（跨链）
	for _, cid := range sortedChainsByPreference(sellChains) {
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
