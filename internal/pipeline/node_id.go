package pipeline

import "strings"

// 节点 ID 约定（与 Node 实现一致，不写死具体链或交易所）：
// - 链上节点：前缀 "onchain:" 或 "onchain-"（后者为自动生成 ID 格式），后接 chainID 等；
// - 交易所节点：无统一前缀，为交易所类型小写（如 binance, okex）。
// 所有「是否为链上节点」「从节点 ID 取 chainID」的逻辑应通过本文件提供的函数判断，避免在节点/边逻辑中写死字符串。

const (
	// NodeIDPrefixOnchain 链上节点 ID 前缀（配置/类型格式，如 "onchain:56"）
	NodeIDPrefixOnchain = "onchain:"
	// NodeIDPrefixOnchainAlt 链上节点 ID 另一前缀（自动生成格式，如 "onchain-56-ZAMA"）
	NodeIDPrefixOnchainAlt = "onchain-"
)

// IsOnchainNodeID 判断节点 ID 是否表示链上节点（不依赖具体链 ID）
func IsOnchainNodeID(nodeID string) bool {
	return strings.HasPrefix(nodeID, NodeIDPrefixOnchain) || strings.HasPrefix(nodeID, NodeIDPrefixOnchainAlt)
}

// ChainIDFromNodeID 从节点 ID 解析链 ID；非链上节点返回空字符串。
// 支持 "onchain:56"、"onchain:1-2"（取首段 "1"）与 "onchain-56-ZAMA" 两种格式。
func ChainIDFromNodeID(nodeID string) string {
	if strings.HasPrefix(nodeID, NodeIDPrefixOnchainAlt) {
		parts := strings.SplitN(strings.TrimPrefix(nodeID, NodeIDPrefixOnchainAlt), "-", 2)
		if len(parts) > 0 && parts[0] != "" {
			return parts[0]
		}
	}
	if strings.HasPrefix(nodeID, NodeIDPrefixOnchain) {
		raw := strings.TrimPrefix(nodeID, NodeIDPrefixOnchain)
		return NormalizeChainID(raw)
	}
	return ""
}

// NormalizeChainID 将可能带后缀的 chainID 规范为纯数字链 ID（如 "1-2" -> "1"），供跨链 CCIP/bridge 使用。
func NormalizeChainID(chainID string) string {
	if chainID == "" {
		return chainID
	}
	if idx := strings.Index(chainID, "-"); idx > 0 && idx < len(chainID) {
		first := strings.TrimSpace(chainID[:idx])
		if first != "" {
			allDigits := true
			for _, c := range first {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				return first
			}
		}
	}
	return chainID
}

// OnchainNodeID 根据链 ID 构造链上节点 ID（用于路由图等，与 buildRouteGraph 约定一致）
func OnchainNodeID(chainID string) string {
	if chainID == "" {
		return ""
	}
	return NodeIDPrefixOnchain + chainID
}

// ParseBrickPipelineName 从搬砖 pipeline 名称解析 symbol、triggerID 与 direction。
// 例："brick-POWERUSDT-forward" -> ("POWERUSDT","","forward",true)；"brick-POWERUSDT-918-backward" -> ("POWERUSDT","918","backward",true)。
// 供 web 层按「已存在的 pipeline」做自动充提轮询，避免按每个 triggerId 查不存在的 pipeline 导致刷屏。
func ParseBrickPipelineName(name string) (symbol, triggerIDStr, direction string, ok bool) {
	if name == "" || !strings.HasPrefix(name, "brick-") {
		return "", "", "", false
	}
	rest := strings.TrimPrefix(name, "brick-")
	if strings.HasSuffix(rest, "-forward") {
		direction = "forward"
		rest = rest[:len(rest)-len("-forward")]
	} else if strings.HasSuffix(rest, "-backward") {
		direction = "backward"
		rest = rest[:len(rest)-len("-backward")]
	} else {
		return "", "", "", false
	}
	if rest == "" {
		return "", "", "", false
	}
	parts := strings.SplitN(rest, "-", 2)
	if len(parts) == 1 {
		return parts[0], "", direction, true
	}
	return parts[0], parts[1], direction, true
}
