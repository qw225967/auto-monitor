package trigger

import (
	"auto-arbitrage/constants"
	"auto-arbitrage/internal/analytics"
	"auto-arbitrage/internal/utils/test"
	"testing"
	"time"
)

func TestNewTriggerManager(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:9876")()

	tm := NewTriggerManager()

	futureSymbols := []string{
		"RAVEUSDT", // 根据 trigger.go 中的配置更新为 TRADOORUSDT
	}

	for _, symbol := range futureSymbols {
		trader := tm.getExchangeSourceInternal(constants.ExchangeBinance)
		tg := tm.NewTriggerWithMode(symbol, nil, trader, ModeScheduled)
		tg = tg.SetSlippageOpt(defaultSlippageOpt()).SetIntervalOpt(defaultIntervalOpt()).SetOrderOpt(defaultOrderOpt())
		tm.addTriggerInternal(symbol, tg)

		// 等待链上客户端初始化（wrapperOnChainSubscribe 会创建链上客户端）
		t.Logf("Waiting for onchain client initialization...")
		time.Sleep(2 * time.Second)

		// 等待链上客户端准备好
		onChainClient := tg.GetOnChainClient()
		if onChainClient == nil {
			t.Logf("Warning: OnChain client is nil, waiting more time...")
			time.Sleep(3 * time.Second)
			onChainClient = tg.GetOnChainClient()
		}

		if onChainClient != nil {
			// OrderAdapter 已废弃，Trigger 使用 sourceA/sourceB 直接下单
			t.Logf("OnChain client ready for symbol: %s (OrderAdapter deprecated)", symbol)
		} else {
			t.Logf("Warning: OnChain client is still nil after waiting, skipping OrderAdapter setup for symbol: %s", symbol)
		}

		// TODO:测试展示代码，后续移除
		// 启动可视化（浏览器访问 http://localhost:8080）
		_ = analytics.NewVisualizer(tg.GetAnalytics()).Start(":8080")
		// TODO:测试展示代码，后续移除
	}

	tm.run()

	// 等待一段时间让系统启动并收集数据
	t.Logf("Waiting for system to start and collect data...")
	time.Sleep(10 * time.Second)

	// 启动所有 trigger 的下单循环
	t.Logf("Starting order loops for all triggers...")
	tm.triggers.Range(func(key, value interface{}) bool {
		if trigger, ok := value.(*Trigger); ok {
			ctx := tm.context
			err := trigger.Start(ctx)
			if err != nil {
				t.Logf("Error starting trigger %d: %v", trigger.ID, err)
			} else {
				t.Logf("Trigger %d started successfully", trigger.ID)
			}
		}
		return true
	})

	// 运行足够长的时间来观察下单流程
	t.Logf("Running for 300 seconds to observe order execution...")
	time.Sleep(time.Second * 30000)

	tm.stop()
	t.Logf("Test completed")
}
