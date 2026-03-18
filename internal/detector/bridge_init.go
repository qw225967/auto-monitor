// Package detector - 跨链桥管理器初始化，用于通路探测时的跨链段验证
//
// 跨链协议需经 bridge.Manager.GetBridgeQuote 验证（CheckBridgeReady + GetQuote），
// 只有通过验证的协议才会展示在 PhysicalFlow 中。
package detector

import (
	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/onchain/bridge"
	"github.com/qw225967/auto-monitor/internal/onchain/bridge/ccip"
	"github.com/qw225967/auto-monitor/internal/onchain/bridge/layerzero"
	"github.com/qw225967/auto-monitor/internal/onchain/bridge/wormhole"
)

// NewBridgeManagerForDetect 创建用于通路探测的跨链桥管理器
// 注册 LayerZero（支持 OFT API 自动拉取）、Wormhole、CCIP
// RPC 使用 constants 中的默认配置
func NewBridgeManagerForDetect() *bridge.Manager {
	rpcMap := buildDefaultRPCMap()
	oftReg := bridge.NewOFTRegistry()
	lz := layerzero.NewLayerZero(rpcMap, true)
	lz.SetOFTRegistry(oftReg)

	wh := wormhole.NewWormhole(rpcMap, true)
	ccipInst := ccip.NewCCIP(rpcMap, true)
	for cid := range rpcMap {
		if urls := constants.GetDefaultRPCURLs(cid); len(urls) > 0 {
			ccipInst.SetRPCURLsForChain(cid, urls)
		}
	}

	mgr := bridge.NewManager(true)
	mgr.RegisterProtocol(lz)
	mgr.RegisterProtocol(wh)
	mgr.RegisterProtocol(ccipInst)
	return mgr
}

func buildDefaultRPCMap() map[string]string {
	out := make(map[string]string)
	for cid := range constants.GetAllDefaultRPCURLs() {
		if u := constants.GetDefaultRPCURL(cid); u != "" {
			out[cid] = u
		}
	}
	return out
}
