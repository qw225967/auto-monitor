// Package detector - 内嵌精简版 RouteProbe，避免依赖完整 pipeline（exchange_node/position 等）
package detector

import (
	"strconv"
	"strings"

	"github.com/qw225967/auto-monitor/internal/detector/registry"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/onchain/bridge"
)

const maxRouteProbeHops = 4

// routeProbe 执行提币路由探测：依次根据节点和边校验每条路径是否可达
// reg 用于校验提现/充值段（交易所是否支持该链）
func routeProbe(req *model.RouteProbeRequest, bridgeManager *bridge.Manager, reg registry.NetworkRegistry) (*model.RouteProbeResult, error) {
	path := req.Path
	if len(path) == 0 && req.Source != "" && req.Destination != "" {
		path = []string{req.Source, req.Destination}
	}
	if len(path) < 2 {
		return &model.RouteProbeResult{Path: path, Segments: []model.SegmentProbeResult{}}, nil
	}
	if len(path) > maxRouteProbeHops {
		path = path[:maxRouteProbeHops]
	}

	probeAmount := req.ProbeAmount
	if probeAmount == "" {
		probeAmount = "100"
	}
	symbol := req.Symbol
	if symbol == "" {
		symbol = "USDT"
	}

	segments := make([]model.SegmentProbeResult, 0, len(path)-1)
	for i := 0; i < len(path)-1; i++ {
		fromID := path[i]
		toID := path[i+1]
		fromChain := chainIDFromNodeID(fromID)
		toChain := chainIDFromNodeID(toID)

		seg := model.SegmentProbeResult{
			FromNodeID:       fromID,
			ToNodeID:         toID,
			Fee:              "0",
			EstimatedTimeSec: 120,
			Available:        true,
		}

		if fromChain != "" && toChain != "" && fromChain != toChain {
			seg.Type = model.SegmentTypeBridge
			if bridgeManager != nil {
				quoteReq := &model.BridgeQuoteRequest{
					FromChain: fromChain,
					ToChain:   toChain,
					FromToken: symbol,
					ToToken:   symbol,
					Amount:    probeAmount,
				}
				quote, err := bridgeManager.GetBridgeQuote(quoteReq)
				if err != nil || quote == nil || len(quote.Protocols) == 0 {
					seg.Available = false
				} else if len(quote.Protocols) > 0 {
					pq := &quote.Protocols[0]
					seg.BridgeProtocol = pq.Protocol
					if !pq.Supported {
						seg.Available = false
					}
				}
			} else {
				seg.Available = false
			}
		} else {
			if fromChain == "" && toChain == "" {
				seg.Type = model.SegmentTypeExchangeToExchange
				seg.EstimatedTimeSec = 60
			} else if fromChain == "" && toChain != "" {
				seg.Type = model.SegmentTypeWithdraw
				seg.EstimatedTimeSec = 60
				// 校验：源交易所（fromID）是否支持向该链提现
				if reg != nil && !canWithdrawToChain(reg, fromID, symbol, toChain) {
					seg.Available = false
				}
			} else {
				seg.Type = model.SegmentTypeDeposit
				seg.EstimatedTimeSec = 60
				// 校验：目标交易所（toID）是否支持从该链充值
				if reg != nil && !canDepositFromChain(reg, toID, symbol, fromChain) {
					seg.Available = false
				}
			}
		}
		segments = append(segments, seg)
	}

	path, segments = mergeExchangeChainExchange(path, segments)
	var totalTime int64
	totalFee := "0"
	for _, s := range segments {
		totalTime += s.EstimatedTimeSec
		if s.Fee != "" && s.Fee != "0" {
			if f, err := strconv.ParseFloat(s.Fee, 64); err == nil {
				if tf, err := strconv.ParseFloat(totalFee, 64); err == nil {
					totalFee = strconv.FormatFloat(tf+f, 'f', -1, 64)
				}
			}
		}
	}

	return &model.RouteProbeResult{
		Path:                  path,
		Segments:              segments,
		TotalEstimatedTimeSec: totalTime,
		TotalFee:              totalFee,
	}, nil
}

func chainIDFromNodeID(nodeID string) string {
	if strings.HasPrefix(nodeID, "onchain:") {
		return strings.TrimPrefix(nodeID, "onchain:")
	}
	if strings.HasPrefix(nodeID, "onchain-") {
		parts := strings.SplitN(strings.TrimPrefix(nodeID, "onchain-"), "-", 2)
		if len(parts) > 0 && parts[0] != "" {
			return parts[0]
		}
	}
	return ""
}

func mergeExchangeChainExchange(path []string, segments []model.SegmentProbeResult) ([]string, []model.SegmentProbeResult) {
	if len(segments) < 2 {
		return path, segments
	}
	var outPath []string
	var outSegs []model.SegmentProbeResult
	outPath = append(outPath, path[0])
	for i := 0; i < len(segments); i++ {
		cur := segments[i]
		fromChain := chainIDFromNodeID(cur.FromNodeID)
		toChain := chainIDFromNodeID(cur.ToNodeID)
		if fromChain == "" && toChain != "" && i+1 < len(segments) {
			next := segments[i+1]
			nextToChain := chainIDFromNodeID(next.ToNodeID)
			if cur.ToNodeID == next.FromNodeID && nextToChain == "" {
				merged := model.SegmentProbeResult{
					FromNodeID:             cur.FromNodeID,
					ToNodeID:               next.ToNodeID,
					Type:                   model.SegmentTypeExchangeToExchange,
					WithdrawNetworkChainID: toChain,
					Fee:                    cur.Fee,
					EstimatedTimeSec:       cur.EstimatedTimeSec + next.EstimatedTimeSec,
					Available:              cur.Available && next.Available,
				}
				outSegs = append(outSegs, merged)
				outPath = append(outPath, next.ToNodeID)
				i++
				continue
			}
		}
		outSegs = append(outSegs, cur)
		if i+1 < len(path) {
			outPath = append(outPath, path[i+1])
		}
	}
	return outPath, outSegs
}

// canWithdrawToChain 校验交易所是否支持向指定链提现
func canWithdrawToChain(reg registry.NetworkRegistry, exchangeType, asset, chainID string) bool {
	nets, err := reg.GetWithdrawNetworks(exchangeType, asset)
	if err != nil || len(nets) == 0 {
		return false
	}
	chainID = strings.TrimSpace(chainID)
	for _, n := range nets {
		if strings.TrimSpace(n.ChainID) == chainID && n.WithdrawEnable {
			return true
		}
	}
	return false
}

// canDepositFromChain 校验交易所是否支持从指定链充值（列表已过滤为可充币网络）
func canDepositFromChain(reg registry.NetworkRegistry, exchangeType, asset, chainID string) bool {
	nets, err := reg.GetDepositNetworks(exchangeType, asset)
	if err != nil || len(nets) == 0 {
		return false
	}
	chainID = strings.TrimSpace(chainID)
	for _, n := range nets {
		if strings.TrimSpace(n.ChainID) == chainID {
			return true
		}
	}
	return false
}
