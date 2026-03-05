package okx

import (
	"testing"

	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/utils/test"
)

// ⚠️ 使用说明：
// 1. 请将下方 testOKXAPIKey / testOKXSecretKey / testOKXPassphrase 改为你自己的 OKX 测试用 API 凭证
// 2. 保持占位符时相关测试会 Skip，不会调用真实 API
// 3. 运行命令：go test -v -run TestOkx
// 4. 注意：Withdraw 测试会实际发起提币请求，请谨慎使用

// 测试用 OKX API 凭证（直接写在代码中，请勿提交真实密钥到仓库）
const (
	testOKXAPIKey     = "your-api-key-here"
	testOKXSecretKey  = ""
	testOKXPassphrase = ""
	testProxyURL      = "http://127.0.0.1:7897"
)

// setupTestOkx 创建并初始化测试用的 OKX 实例（使用上方常量中的 API 凭证）
func setupTestOkx(t *testing.T) *okx {
	if testOKXAPIKey == "your-api-key-here" {
		t.Skip("⚠️ 请先在测试文件中配置 testOKXAPIKey, testOKXSecretKey, testOKXPassphrase")
	}

	// 初始化 logger
	logger.InitLogger("")

	// 初始化配置（确保 config 已初始化）
	config.InitSelfConfigFromDefault()

	// 创建 OKX 实例
	okxEx := NewOkx().(*okx)

	// 手动设置 APIKey（通过修改全局配置）
	globalConfig := config.GetGlobalConfig()
	if globalConfig == nil {
		t.Fatalf("❌ 无法获取全局配置")
	}
	globalConfig.OkEx.KeyList = []config.OkExKeyRecord{
		{
			APIKey:       testOKXAPIKey,
			Secret:       testOKXSecretKey,
			Passphrase:   testOKXPassphrase,
			CanBroadcast: true,
		},
	}

	// 初始化
	err := okxEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	return okxEx
}

// TestOkxInit 测试 OKX 初始化
func TestOkxInit(t *testing.T) {
	defer test.SetupProxyForTest(testProxyURL)()

	if testOKXAPIKey == "your-api-key-here" {
		t.Skip("⚠️ 请先在测试文件中配置 testOKXAPIKey, testOKXSecretKey, testOKXPassphrase")
	}

	t.Log("🔧 测试 OKX 初始化...")

	okxEx := setupTestOkx(t)

	if okxEx.GetType() != "okex" {
		t.Errorf("❌ GetType() 返回错误: got %s, want okex", okxEx.GetType())
	}

	t.Log("✅ OKX 初始化成功")
}

// TestOkxDeposit 测试获取充币地址
func TestOkxDeposit(t *testing.T) {
	defer test.SetupProxyForTest(testProxyURL)()

	if testOKXAPIKey == "your-api-key-here" {
		t.Skip("⚠️ 请先在测试文件中配置 testOKXAPIKey, testOKXSecretKey, testOKXPassphrase")
	}

	t.Log("💰 测试 OKX 获取充币地址...")

	okxEx := setupTestOkx(t)

	// 测试用例 1: 获取 USDT 充币地址（不指定网络）
	t.Run("USDT without network", func(t *testing.T) {
		addr, err := okxEx.Deposit("USDT", "")
		if err != nil {
			t.Fatalf("❌ 获取 USDT 充币地址失败: %v", err)
		}

		if addr == nil {
			t.Fatal("❌ 返回的充币地址为 nil")
		}

		t.Logf("✅ USDT 充币地址:")
		t.Logf("   资产: %s", addr.Asset)
		t.Logf("   地址: %s", addr.Address)
		t.Logf("   网络: %s", addr.Network)
		if addr.Memo != "" {
			t.Logf("   Memo/Tag: %s", addr.Memo)
		}
	})

	// 测试用例 2: 获取 USDT 充币地址（指定 ERC20 网络）
	t.Run("USDT with ERC20 network", func(t *testing.T) {
		addr, err := okxEx.Deposit("USDT", "ERC20")
		if err != nil {
			t.Fatalf("❌ 获取 USDT (ERC20) 充币地址失败: %v", err)
		}

		if addr == nil {
			t.Fatal("❌ 返回的充币地址为 nil")
		}

		t.Logf("✅ USDT (ERC20) 充币地址:")
		t.Logf("   资产: %s", addr.Asset)
		t.Logf("   地址: %s", addr.Address)
		t.Logf("   网络: %s", addr.Network)
		if addr.Memo != "" {
			t.Logf("   Memo/Tag: %s", addr.Memo)
		}
	})

	// 测试用例 3: 获取 USDT 充币地址（指定 BSC 网络）
	//t.Run("USDT with BSC network", func(t *testing.T) {
	//	addr, err := okxEx.Deposit("USDT", "BEP20")
	//	if err != nil {
	//		t.Fatalf("❌ 获取 USDT (BSC) 充币地址失败: %v", err)
	//	}
	//
	//	if addr == nil {
	//		t.Fatal("❌ 返回的充币地址为 nil")
	//	}
	//
	//	t.Logf("✅ USDT (BSC) 充币地址:")
	//	t.Logf("   资产: %s", addr.Asset)
	//	t.Logf("   地址: %s", addr.Address)
	//	t.Logf("   网络: %s", addr.Network)
	//	if addr.Memo != "" {
	//		t.Logf("   Memo/Tag: %s", addr.Memo)
	//	}
	//})

	// 测试用例 4: 获取 ZAMA 充币地址
	t.Run("ZAMA deposit address", func(t *testing.T) {
		addr, err := okxEx.Deposit("ZAMA", "")
		if err != nil {
			t.Fatalf("❌ 获取 ZAMA 充币地址失败: %v", err)
		}

		if addr == nil {
			t.Fatal("❌ 返回的充币地址为 nil")
		}

		t.Logf("✅ ZAMA 充币地址:")
		t.Logf("   资产: %s", addr.Asset)
		t.Logf("   地址: %s", addr.Address)
		t.Logf("   网络: %s", addr.Network)
		if addr.Memo != "" {
			t.Logf("   Memo/Tag: %s", addr.Memo)
		}
	})
}

// TestOkxGetDepositHistory 测试查询充币记录
func TestOkxGetDepositHistory(t *testing.T) {
	defer test.SetupProxyForTest(testProxyURL)()

	if testOKXAPIKey == "your-api-key-here" {
		t.Skip("⚠️ 请先在测试文件中配置 testOKXAPIKey, testOKXSecretKey, testOKXPassphrase")
	}

	t.Log("📜 测试 OKX 查询充币记录...")

	okxEx := setupTestOkx(t)

	// 测试用例 1: 查询所有资产的充币记录
	t.Run("All assets deposit history", func(t *testing.T) {
		records, err := okxEx.GetDepositHistory("", 10)
		if err != nil {
			t.Fatalf("❌ 查询充币记录失败: %v", err)
		}

		t.Logf("✅ 查询到 %d 条充币记录", len(records))
		for i, record := range records {
			t.Logf("   记录 %d:", i+1)
			t.Logf("     交易哈希: %s", record.TxHash)
			t.Logf("     资产: %s", record.Asset)
			t.Logf("     数量: %.8f", record.Amount)
			t.Logf("     网络: %s", record.Network)
			t.Logf("     状态: %s", record.Status)
			t.Logf("     时间: %s", record.CreateTime.Format("2006-01-02 15:04:05"))
		}
	})

	// 测试用例 2: 查询 USDT 的充币记录
	t.Run("USDT deposit history", func(t *testing.T) {
		records, err := okxEx.GetDepositHistory("USDT", 5)
		if err != nil {
			t.Fatalf("❌ 查询 USDT 充币记录失败: %v", err)
		}

		t.Logf("✅ 查询到 %d 条 USDT 充币记录", len(records))
		for i, record := range records {
			t.Logf("   记录 %d:", i+1)
			t.Logf("     交易哈希: %s", record.TxHash)
			t.Logf("     数量: %.8f USDT", record.Amount)
			t.Logf("     网络: %s", record.Network)
			t.Logf("     状态: %s", record.Status)
			t.Logf("     时间: %s", record.CreateTime.Format("2006-01-02 15:04:05"))
		}
	})
}

// TestOkxGetWithdrawHistory 测试查询提币记录
func TestOkxGetWithdrawHistory(t *testing.T) {
	defer test.SetupProxyForTest(testProxyURL)()

	if testOKXAPIKey == "your-api-key-here" {
		t.Skip("⚠️ 请先在测试文件中配置 testOKXAPIKey, testOKXSecretKey, testOKXPassphrase")
	}

	t.Log("📜 测试 OKX 查询提币记录...")

	okxEx := setupTestOkx(t)

	// 测试用例 1: 查询所有资产的提币记录
	t.Run("All assets withdraw history", func(t *testing.T) {
		records, err := okxEx.GetWithdrawHistory("", 10)
		if err != nil {
			t.Fatalf("❌ 查询提币记录失败: %v", err)
		}

		t.Logf("✅ 查询到 %d 条提币记录", len(records))
		for i, record := range records {
			t.Logf("   记录 %d:", i+1)
			t.Logf("     提币ID: %s", record.WithdrawID)
			t.Logf("     交易哈希: %s", record.TxHash)
			t.Logf("     资产: %s", record.Asset)
			t.Logf("     数量: %.8f", record.Amount)
			t.Logf("     网络: %s", record.Network)
			t.Logf("     地址: %s", record.Address)
			t.Logf("     状态: %s", record.Status)
			t.Logf("     时间: %s", record.CreateTime.Format("2006-01-02 15:04:05"))
		}
	})

	// 测试用例 2: 查询 USDT 的提币记录
	t.Run("USDT withdraw history", func(t *testing.T) {
		records, err := okxEx.GetWithdrawHistory("USDT", 5)
		if err != nil {
			t.Fatalf("❌ 查询 USDT 提币记录失败: %v", err)
		}

		t.Logf("✅ 查询到 %d 条 USDT 提币记录", len(records))
		for i, record := range records {
			t.Logf("   记录 %d:", i+1)
			t.Logf("     提币ID: %s", record.WithdrawID)
			t.Logf("     交易哈希: %s", record.TxHash)
			t.Logf("     数量: %.8f USDT", record.Amount)
			t.Logf("     网络: %s", record.Network)
			t.Logf("     地址: %s", record.Address)
			t.Logf("     状态: %s", record.Status)
			t.Logf("     时间: %s", record.CreateTime.Format("2006-01-02 15:04:05"))
		}
	})
}

// TestOkxGetWithdrawNetworks 测试查询支持的提现网络
func TestOkxGetWithdrawNetworks(t *testing.T) {
	defer test.SetupProxyForTest(testProxyURL)()

	if testOKXAPIKey == "your-api-key-here" {
		t.Skip("⚠️ 请先在测试文件中配置 testOKXAPIKey, testOKXSecretKey, testOKXPassphrase")
	}

	t.Log("🌐 测试 OKX 查询支持的提现网络...")

	okxEx := setupTestOkx(t)

	// 测试用例 1: 查询 USDT 支持的提现网络
	t.Run("USDT withdraw networks", func(t *testing.T) {
		networks, err := okxEx.GetWithdrawNetworks("USDT")
		if err != nil {
			t.Fatalf("❌ 查询 USDT 提现网络失败: %v", err)
		}

		if len(networks) == 0 {
			t.Log("⚠️  未查询到 USDT 支持的提现网络")
			return
		}

		t.Logf("✅ 查询到 %d 个 USDT 支持的提现网络:", len(networks))
		for i, network := range networks {
			t.Logf("   网络 %d:", i+1)
			t.Logf("     网络名称: %s", network.Network)
			t.Logf("     链ID: %s", network.ChainID)
			t.Logf("     是否支持提现: %v", network.WithdrawEnable)
			t.Logf("     提现手续费: %s", network.WithdrawFee)
			t.Logf("     最小提现金额: %s", network.WithdrawMin)
			t.Logf("     最大提现金额: %s", network.WithdrawMax)
			t.Logf("     是否默认网络: %v", network.IsDefault)
		}
	})

	// 测试用例 2: 查询 BTC 支持的提现网络
	t.Run("BTC withdraw networks", func(t *testing.T) {
		networks, err := okxEx.GetWithdrawNetworks("BTC")
		if err != nil {
			t.Fatalf("❌ 查询 BTC 提现网络失败: %v", err)
		}

		if len(networks) == 0 {
			t.Log("⚠️  未查询到 BTC 支持的提现网络")
			return
		}

		t.Logf("✅ 查询到 %d 个 BTC 支持的提现网络:", len(networks))
		for i, network := range networks {
			t.Logf("   网络 %d:", i+1)
			t.Logf("     网络名称: %s", network.Network)
			t.Logf("     链ID: %s", network.ChainID)
			t.Logf("     是否支持提现: %v", network.WithdrawEnable)
			t.Logf("     提现手续费: %s", network.WithdrawFee)
			t.Logf("     最小提现金额: %s", network.WithdrawMin)
			t.Logf("     最大提现金额: %s", network.WithdrawMax)
			t.Logf("     是否默认网络: %v", network.IsDefault)
		}
	})

	// 测试用例 3: 查询 ETH 支持的提现网络
	t.Run("ETH withdraw networks", func(t *testing.T) {
		networks, err := okxEx.GetWithdrawNetworks("ETH")
		if err != nil {
			t.Fatalf("❌ 查询 ETH 提现网络失败: %v", err)
		}

		if len(networks) == 0 {
			t.Log("⚠️  未查询到 ETH 支持的提现网络")
			return
		}

		t.Logf("✅ 查询到 %d 个 ETH 支持的提现网络:", len(networks))
		for i, network := range networks {
			t.Logf("   网络 %d:", i+1)
			t.Logf("     网络名称: %s", network.Network)
			t.Logf("     链ID: %s", network.ChainID)
			t.Logf("     是否支持提现: %v", network.WithdrawEnable)
			t.Logf("     提现手续费: %s", network.WithdrawFee)
			t.Logf("     最小提现金额: %s", network.WithdrawMin)
			t.Logf("     最大提现金额: %s", network.WithdrawMax)
			t.Logf("     是否默认网络: %v", network.IsDefault)
		}
	})

	// 测试用例 4: 查询 ZAMA 支持的提现网络
	t.Run("ZAMA withdraw networks", func(t *testing.T) {
		networks, err := okxEx.GetWithdrawNetworks("ZAMA")
		if err != nil {
			t.Fatalf("❌ 查询 ZAMA 提现网络失败: %v", err)
		}

		if len(networks) == 0 {
			t.Log("⚠️  未查询到 ZAMA 支持的提现网络")
			return
		}

		t.Logf("✅ 查询到 %d 个 ZAMA 支持的提现网络:", len(networks))
		for i, network := range networks {
			t.Logf("   网络 %d:", i+1)
			t.Logf("     网络名称: %s", network.Network)
			t.Logf("     链ID: %s", network.ChainID)
			t.Logf("     是否支持提现: %v", network.WithdrawEnable)
			t.Logf("     提现手续费: %s", network.WithdrawFee)
			t.Logf("     最小提现金额: %s", network.WithdrawMin)
			t.Logf("     最大提现金额: %s", network.WithdrawMax)
			t.Logf("     是否默认网络: %v", network.IsDefault)
		}
	})
}

// TestOkxWithdraw 测试提币功能
// 默认测试不带 RcvrInfo（普通用户提币）。特定主体用户需设置 RcvrInfo，其中 walletType 为 rcvrInfo 的必填字段：exchange=提币到交易所钱包，private=提币到私人钱包；接收方为公司时 rcvrFirstName 填公司名称，rcvrLastName 填 "N/A"，地址可填公司注册地址。
// ⚠️ 警告：此测试会实际发起提币请求，请谨慎使用！
func TestOkxWithdraw(t *testing.T) {
	defer test.SetupProxyForTest(testProxyURL)()

	if testOKXAPIKey == "your-api-key-here" {
		t.Skip("⚠️ 请先在测试文件中配置 testOKXAPIKey, testOKXSecretKey, testOKXPassphrase")
	}

	// 默认跳过此测试，避免误操作
	//t.Skip("⚠️  提币测试已跳过，如需测试请注释掉本处 t.Skip 并修改下方测试参数")

	t.Log("💸 测试 OKX 提币功能（默认不带 RcvrInfo）...")
	t.Log("⚠️  警告：此测试会实际发起提币请求！")

	okxEx := setupTestOkx(t)

	// 测试用例：普通用户提币，不设置 RcvrInfo（walletType 为 rcvrInfo 内字段，此处不涉及）
	// ⚠️ 请修改以下参数为你的测试地址和金额
	withdrawReq := &model.WithdrawRequest{
		Asset:   "USDT",
		Amount:  10.0,                                         // 测试金额，请使用小额
		Address: "0xdbb291b95e0fc3aaa3385375d3ba4be0572939fd", // 请替换为你的测试地址
		Network: "ERC20",                                      // 或 "BEP20", "TRC20" 等
		Memo:    "",                                           // 某些链需要 memo/tag
		// 特定主体用户需填 RcvrInfo，其中 walletType 必填（exchange=交易所钱包，private=私人钱包）；
		// 接收方为公司时：RcvrFirstName=公司名称，RcvrLastName="N/A"，Address=公司注册地址。
		// RcvrInfo: &model.WithdrawRcvrInfo{WalletType: "exchange", ExchId: "did:ethr:0x...", RcvrFirstName: "Bruce", RcvrLastName: "Wayne"},
	}

	t.Logf("📤 发起提币请求（无 RcvrInfo）:")
	t.Logf("   资产: %s", withdrawReq.Asset)
	t.Logf("   数量: %.8f", withdrawReq.Amount)
	t.Logf("   地址: %s", withdrawReq.Address)
	t.Logf("   网络: %s", withdrawReq.Network)

	resp, err := okxEx.Withdraw(withdrawReq)
	if err != nil {
		t.Fatalf("❌ 提币失败: %v", err)
	}

	if resp == nil {
		t.Fatal("❌ 返回的提币响应为 nil")
	}

	t.Logf("✅ 提币请求成功:")
	t.Logf("   提币ID: %s", resp.WithdrawID)
	t.Logf("   状态: %s", resp.Status)
	t.Logf("   创建时间: %s", resp.CreateTime.Format("2006-01-02 15:04:05"))
}

// TestOkxErrorHandling 测试错误处理
func TestOkxErrorHandling(t *testing.T) {
	defer test.SetupProxyForTest(testProxyURL)()

	if testOKXAPIKey == "your-api-key-here" {
		t.Skip("⚠️ 请先在测试文件中配置 testOKXAPIKey, testOKXSecretKey, testOKXPassphrase")
	}

	t.Log("🔍 测试 OKX 错误处理...")

	// 测试用例 1: 未初始化的实例
	t.Run("Uninitialized instance", func(t *testing.T) {
		okxEx := NewOkx().(*okx)
		// 不调用 Init()

		_, err := okxEx.Deposit("USDT", "")
		if err != exchange.ErrNotInitialized {
			t.Errorf("❌ 期望 ErrNotInitialized，但得到: %v", err)
		} else {
			t.Log("✅ 未初始化错误处理正确")
		}
	})

	// 测试用例 2: 空请求
	t.Run("Nil withdraw request", func(t *testing.T) {
		okxEx := setupTestOkx(t)

		_, err := okxEx.Withdraw(nil)
		if err == nil {
			t.Error("❌ 期望错误，但得到 nil")
		} else {
			t.Logf("✅ 空请求错误处理正确: %v", err)
		}
	})

	// 测试用例 3: 无效资产
	t.Run("Invalid asset", func(t *testing.T) {
		okxEx := setupTestOkx(t)

		// 使用不存在的资产
		_, err := okxEx.Deposit("INVALIDASSET123", "")
		if err == nil {
			t.Error("❌ 期望错误，但得到 nil")
		} else {
			t.Logf("✅ 无效资产错误处理正确: %v", err)
		}
	})
}

// TestOkxNetworkMapping 测试网络名称映射
func TestOkxNetworkMapping(t *testing.T) {
	t.Log("🔄 测试 OKX 网络名称映射...")

	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{"BEP20 to BSC", "BEP20", "BSC"},
		{"ERC20 to ETH", "ERC20", "ETH"},
		{"TRC20 to TRX", "TRC20", "TRX"},
		{"POLYGON to MATIC", "POLYGON", "MATIC"},
		{"Unknown network", "UNKNOWN", "UNKNOWN"},
		{"Empty network", "", ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := mapNetworkName(tc.input)
			if result != tc.expected {
				t.Errorf("❌ 映射错误: 输入=%s, 期望=%s, 得到=%s", tc.input, tc.expected, result)
			} else {
				t.Logf("✅ %s: %s -> %s", tc.name, tc.input, result)
			}
		})
	}
}
