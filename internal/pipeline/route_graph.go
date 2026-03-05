package pipeline

// FindAllRoutes 在图上从 source 到 dest 枚举所有路径，最多 maxHops 跳。
// adj 为邻接表：adj[nodeID] = 可直达的下一跳节点 ID 列表。
// 节点 ID 约定见 node_id.go（链上节点用 OnchainNodeID(chainID)，交易所为类型小写）；与具体链/交易所无关。
func FindAllRoutes(source, dest string, adj map[string][]string, maxHops int) [][]string {
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
		neighbors := adj[cur]
		for _, next := range neighbors {
			key := next
			if visited[key] {
				continue
			}
			visited[key] = true
			dfs(next, hops+1)
			visited[key] = false
		}
	}

	visited[source] = true
	dfs(source, 0)
	visited[source] = false
	return out
}
