package bundler

// 这是一个使用示例文件，展示如何配置和使用 bundler
// 实际使用时，请根据你的配置修改

/*
import (
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/onchain"
	"auto-arbitrage/internal/onchain/bundler"
)

func ExampleUsage() {
	// 1. 创建 bundler 管理器
	bundlerMgr := bundler.NewManager()

	// 2. 添加 Flashbots bundler (仅支持 Ethereum 主网)
	// 注意：需要提供用于签名请求的私钥（不是交易签名私钥）
	flashbotsBundler, err := bundler.NewFlashbotsBundler(
		config.FlashbotsPrivateKey, // 从配置获取
		"",                          // 使用默认 relay URL
	)
	if err == nil {
		bundlerMgr.AddBundler(flashbotsBundler)
	}

	// 3. 添加 48club bundler (支持 Ethereum 和 BSC)
	// 注意：48SoulPoint 私钥是可选的，但提供后可以获得更好的服务（更高的限流、更多交易等）
	fortyEightClubBundler, err := bundler.NewFortyEightClubBundler(
		config.FortyEightClubAPIKey,        // 从配置获取（可选）
		"",                                  // 使用默认 API URL
		config.FortyEightSoulPointPrivateKey, // 48SoulPoint 成员私钥（可选）
	)
	if err == nil {
		bundlerMgr.AddBundler(fortyEightClubBundler)
	}

	// 4. 创建 okdex 客户端
	client := onchain.NewOkdex()
	err = client.Init()
	if err != nil {
		panic(err)
	}

	// 5. 设置 bundler 管理器并启用
	if okdexClient, ok := client.(*onchain.Okdex); ok {
		okdexClient.SetBundlerManager(bundlerMgr, true) // true 表示启用 bundler
	}

	// 6. 正常使用客户端，系统会自动尝试通过 bundler 发送交易
	// 如果 bundler 失败，会自动 fallback 到普通广播方式
}
*/

