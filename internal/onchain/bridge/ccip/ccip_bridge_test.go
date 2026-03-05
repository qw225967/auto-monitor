// Package ccip 跨链单测：通过环境变量传入私钥和地址，发起一次真实 CCIP 跨链。
//
// 用法：
//
//	CCIP_TEST_PRIVATE_KEY=0x... \
//	CCIP_TEST_TOKEN_ADDR_1=0x... \   # 源链(ETH)代币合约地址
//	CCIP_TEST_TOKEN_ADDR_56=0x... \  # 目标链(BSC)代币合约地址
//	CCIP_TEST_AMOUNT=0.01 \
//	CCIP_TEST_RECIPIENT=0x... \      # 可选，默认用私钥对应地址
//	go test -v -run TestCCIPBridgeManual ./internal/onchain/bridge/ccip/
//
// 可选 RPC 覆盖（用于调试上链问题）：
//
//	CCIP_TEST_RPC_1=https://...   # 覆盖 ETH RPC
//	CCIP_TEST_RPC_56=https://...  # 覆盖 BSC RPC
//
// 若未设置必需环境变量，测试会跳过（Skip）。
package ccip

import (
	"os"
	"testing"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/model"
)

func TestCCIPBridgeManual(t *testing.T) {
	pk := os.Getenv("CCIP_TEST_PRIVATE_KEY")
	tokenAddr1 := "0x9dc44ae5be187eca9e2a67e33f27a4c91cea1223"
	tokenAddr56 := "0x9dc44ae5be187eca9e2a67e33f27a4c91cea1223"
	amount := os.Getenv("CCIP_TEST_AMOUNT")
	recipient := os.Getenv("CCIP_TEST_RECIPIENT")
	rpc1 := os.Getenv("CCIP_TEST_RPC_1")
	rpc56 := os.Getenv("CCIP_TEST_RPC_56")
	tokenSymbol := os.Getenv("CCIP_TEST_TOKEN_SYMBOL")
	if tokenSymbol == "" {
		tokenSymbol = "POWER"
	}

	if pk == "" || tokenAddr1 == "" || tokenAddr56 == "" || amount == "" {
		t.Skip("CCIP manual test skipped: set CCIP_TEST_PRIVATE_KEY, CCIP_TEST_TOKEN_ADDR_1, CCIP_TEST_TOKEN_ADDR_56, CCIP_TEST_AMOUNT to run. Token addresses can be from route probe or config Bridge.CCIP.TokenPools.")
	}

	// 确保 config 已加载，并注入测试私钥
	_ = config.InitSelfConfigFromDefault()
	cfg := config.GetGlobalConfig()
	if cfg == nil {
		t.Fatal("config not initialized")
	}
	cfg.Wallet.PrivateSecret = pk
	if recipient != "" {
		cfg.Wallet.WalletAddress = recipient
	}

	rpcURLs := constants.GetAllDefaultRPCURLs()
	if rpc1 != "" {
		rpcURLs["1"] = rpc1
	}
	if rpc56 != "" {
		rpcURLs["56"] = rpc56
	}
	ccip := NewCCIP(rpcURLs, true)
	for _, cid := range []string{"1", "56"} {
		if urls := constants.GetDefaultRPCURLs(cid); len(urls) > 0 {
			ccip.SetRPCURLsForChain(cid, urls)
		}
	}
	if rpc1 != "" {
		ccip.SetRPCURLsForChain("1", append([]string{rpc1}, constants.GetDefaultRPCURLs("1")...))
	}
	if rpc56 != "" {
		ccip.SetRPCURLsForChain("56", append([]string{rpc56}, constants.GetDefaultRPCURLs("56")...))
	}

	// 配置代币地址（1=ETH, 56=BSC），地址可从 route probe 或 config Bridge.CCIP.TokenPools 获取
	ccip.SetTokenPool("1", tokenSymbol, tokenAddr1)
	ccip.SetTokenPool("56", tokenSymbol, tokenAddr56)

	req := &model.BridgeRequest{
		FromChain: "1",
		ToChain:   "56",
		FromToken: tokenSymbol,
		ToToken:   tokenSymbol,
		Amount:    amount,
		Recipient: recipient,
	}

	t.Logf("CCIP bridge: %s -> %s, token=%s, amount=%s, tokenAddr1=%s, tokenAddr56=%s",
		req.FromChain, req.ToChain, tokenSymbol, req.Amount, tokenAddr1, tokenAddr56)
	if rpc1 != "" {
		t.Logf("Using custom RPC for chain 1")
	}

	resp, err := ccip.BridgeToken(req)
	if err != nil {
		t.Fatalf("BridgeToken failed: %v", err)
	}

	t.Logf("Bridge success: txHash=%s, bridgeID=%s, fee=%s", resp.TxHash, resp.BridgeID, resp.Fee)
}
