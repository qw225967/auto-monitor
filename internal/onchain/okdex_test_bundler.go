package onchain

import (
	"testing"

	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/onchain/bundler"
)

// TestOkdex_BundlerSupport 测试 bundler 支持
func TestOkdex_BundlerSupport(t *testing.T) {
	// 创建 bundler 管理器
	bundlerMgr := bundler.NewManager()

	// 测试 Flashbots bundler（如果配置了）
	globalCfg := config.GetGlobalConfig()
	if globalCfg == nil {
		t.Skip("GlobalConfig not initialized")
	}
	if globalCfg.Bundler.FlashbotsPrivateKey != "" {
		flashbotsBundler, err := bundler.NewFlashbotsBundler(globalCfg.Bundler.FlashbotsPrivateKey, "")
		if err != nil {
			t.Logf("⚠️  Flashbots bundler creation failed: %v", err)
		} else {
			bundlerMgr.AddBundler(flashbotsBundler)
			t.Logf("✅ Flashbots bundler created")

			// 测试链支持
			if flashbotsBundler.SupportsChain("1") {
				t.Logf("✅ Flashbots supports Ethereum (chainID=1)")
			}
			if !flashbotsBundler.SupportsChain("56") {
				t.Logf("✅ Flashbots correctly doesn't support BSC (chainID=56)")
			}
		}
	} else {
		t.Logf("ℹ️  Flashbots private key not configured, skipping Flashbots test")
	}

	// 测试 48club bundler（如果配置了）
	if globalCfg.Bundler.FortyEightClubAPIKey != "" {
		fortyEightClubBundler, err := bundler.NewFortyEightClubBundler(globalCfg.Bundler.FortyEightClubAPIKey, "", globalCfg.Bundler.FortyEightSoulPointPrivateKey)
		if err != nil {
			t.Logf("⚠️  48club bundler creation failed: %v", err)
		} else {
			bundlerMgr.AddBundler(fortyEightClubBundler)
			t.Logf("✅ 48club bundler created")

			// 测试链支持
			if fortyEightClubBundler.SupportsChain("1") {
				t.Logf("✅ 48club supports Ethereum (chainID=1)")
			}
			if fortyEightClubBundler.SupportsChain("56") {
				t.Logf("✅ 48club supports BSC (chainID=56)")
			}
		}
	} else {
		t.Logf("ℹ️  48club API key not configured, skipping 48club test")
	}

	// 测试 bundler 管理器
	if len(bundlerMgr.GetAllBundlers()) > 0 {
		t.Logf("✅ Bundler manager has %d bundler(s)", len(bundlerMgr.GetAllBundlers()))

		// 测试获取 bundler
		ethBundler, err := bundlerMgr.GetBundler("1")
		if err == nil && ethBundler != nil {
			t.Logf("✅ Found bundler for Ethereum: %s", ethBundler.GetName())
		}

		bscBundler, err := bundlerMgr.GetBundler("56")
		if err == nil && bscBundler != nil {
			t.Logf("✅ Found bundler for BSC: %s", bscBundler.GetName())
		}
	} else {
		t.Logf("ℹ️  No bundlers configured, bundler features will not be available")
	}

	// 测试集成到 okdex
	client := NewOkdex()
	err := client.Init()
	if err != nil {
		t.Fatalf("Failed to init okdex: %v", err)
	}

	if okdexClient, ok := client.(*okdex); ok {
		okdexClient.SetBundlerManager(bundlerMgr, true)
		t.Logf("✅ Bundler manager set to okdex client")

		// 验证设置
		okdexClient.mu.RLock()
		useBundler := okdexClient.useBundler
		hasManager := okdexClient.bundlerManager != nil
		okdexClient.mu.RUnlock()

		if useBundler {
			t.Logf("✅ Bundler is enabled")
		}
		if hasManager {
			t.Logf("✅ Bundler manager is set")
		}
	}
}

