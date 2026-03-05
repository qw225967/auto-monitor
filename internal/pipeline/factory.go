package pipeline

import (
	"fmt"
	"strings"

	"auto-arbitrage/internal/onchain"
)

// NodeTypeConstant 节点类型常量（用于工厂函数）
type NodeTypeConstant string

const (
	// 交易所节点类型
	TypeNodeBinance NodeTypeConstant = "binance"
	TypeNodeBybit   NodeTypeConstant = "bybit"
	TypeNodeBitget  NodeTypeConstant = "bitget"
	TypeNodeGate    NodeTypeConstant = "gate"
	TypeNodeOKX     NodeTypeConstant = "okex"
	TypeNodeHyperliquid NodeTypeConstant = "hyperliquid"
	TypeNodeLighter    NodeTypeConstant = "lighter"
	TypeNodeAster      NodeTypeConstant = "aster"

	// 链上节点类型（格式：onchain:链ID）
	TypeNodeOnchainBSC      NodeTypeConstant = "onchain:56"  // BSC
	TypeNodeOnchainETH      NodeTypeConstant = "onchain:1"   // Ethereum
	TypeNodeOnchainPolygon  NodeTypeConstant = "onchain:137" // Polygon
	TypeNodeOnchainArbitrum NodeTypeConstant = "onchain:42161" // Arbitrum
	TypeNodeOnchainOptimism NodeTypeConstant = "onchain:10"  // Optimism
	TypeNodeOnchainAvalanche NodeTypeConstant = "onchain:43114" // Avalanche

	// 跨链已改为边上的行为，不再作为节点类型；保留常量仅用于返回明确错误提示
	TypeNodeBridgeLayerZero NodeTypeConstant = "bridge:layerzero"
	TypeNodeBridgeWormhole  NodeTypeConstant = "bridge:wormhole"
	TypeNodeBridgeAuto      NodeTypeConstant = "bridge:auto"
)

// CreateNode 创建节点（工厂函数）
// 根据节点类型和资产符号创建对应的节点实例
func CreateNode(nodeType NodeTypeConstant, assetSymbol string, configs ...interface{}) (Node, error) {
	switch nodeType {
	case TypeNodeBinance, TypeNodeBybit, TypeNodeBitget, TypeNodeGate, TypeNodeOKX, TypeNodeHyperliquid, TypeNodeLighter, TypeNodeAster:
		return CreateExchangeNode(string(nodeType), assetSymbol)
	case TypeNodeOnchainBSC, TypeNodeOnchainETH, TypeNodeOnchainPolygon, TypeNodeOnchainArbitrum, TypeNodeOnchainOptimism, TypeNodeOnchainAvalanche:
		return CreateOnchainNodeByType(string(nodeType), assetSymbol, configs...)
	case TypeNodeBridgeLayerZero, TypeNodeBridgeWormhole, TypeNodeBridgeAuto:
		return nil, fmt.Errorf("跨链已改为边行为，请使用两个 OnchainNode（不同 chainID）并在边上配置 BridgeProtocol，且对 Pipeline 调用 SetBridgeManager，不要创建 bridge 节点")
	default:
		return nil, fmt.Errorf("unknown node type: %s", nodeType)
	}
}

// CreateExchangeNode 创建交易所节点
func CreateExchangeNode(exchangeType string, assetSymbol string) (Node, error) {
	cfg := ExchangeNodeConfig{
		ExchangeType:   exchangeType,
		Asset:          assetSymbol,
		DefaultNetwork: "ERC20", // 默认网络，可通过配置覆盖
	}
	return NewExchangeNode(cfg), nil
}

// CreateOnchainNodeByType 根据类型字符串创建链上节点
// nodeType 格式：onchain:链ID（如 "onchain:56"）
func CreateOnchainNodeByType(nodeType string, assetSymbol string, configs ...interface{}) (Node, error) {
	// 解析链ID（使用统一约定，不写死前缀长度）
	var chainID string
	if strings.HasPrefix(nodeType, NodeIDPrefixOnchain) {
		chainID = strings.TrimPrefix(nodeType, NodeIDPrefixOnchain)
	} else {
		return nil, fmt.Errorf("invalid onchain node type format: %s (expected onchain:链ID)", nodeType)
	}

	// 从配置中提取参数
	var walletAddress string
	var tokenAddress string
	var client onchain.OnchainClient

	for _, cfg := range configs {
		switch v := cfg.(type) {
		case string:
			if walletAddress == "" {
				walletAddress = v
			} else if tokenAddress == "" {
				tokenAddress = v
			}
		case onchain.OnchainClient:
			client = v
		}
	}

	// 如果没有提供 client，尝试从全局配置获取
	if client == nil {
		// 这里简化处理，实际应该从某个全局管理器获取
		// 暂时返回错误，提示需要提供 client
		return nil, fmt.Errorf("onchain client is required for onchain node")
	}

	// 如果没有提供 walletAddress，尝试从全局配置获取
	if walletAddress == "" {
		// 这里简化处理，实际应该从配置中获取
		return nil, fmt.Errorf("wallet address is required for onchain node")
	}

	cfg := OnchainNodeConfig{
		ChainID:      chainID,
		AssetSymbol:  assetSymbol,
		TokenAddress: tokenAddress,
		WalletAddress: walletAddress,
		Client:      client,
	}
	return NewOnchainNode(cfg), nil
}

// CreateAutoWithdrawPipeline 创建自动提币 pipeline（便捷函数）
// 匹配用户伪代码：pipelineAB = createAutoWithdrawPipeline(createNode(...), createNode(...))
func CreateAutoWithdrawPipeline(name string, nodes ...Node) *Pipeline {
	return NewPipeline(name, nodes...)
}
