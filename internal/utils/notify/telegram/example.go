package telegram

import (
	"github.com/qw225967/auto-monitor/internal/config"
	"time"
)

// ExampleUsage 展示如何使用 Telegram 通知功能
// 注意：这是一个示例文件，实际使用时请确保已配置 Telegram BotToken 和 ChatID
func ExampleUsage() {
	// 方式一：从 GlobalConfig 读取（推荐）
	globalCfg := config.GetGlobalConfig()
	if globalCfg == nil {
		return
	}
	client := NewTelegramClient(
		globalCfg.Telegram.BotToken,
		globalCfg.Telegram.ChatID,
	)

	// 发送普通文本消息
	_, _ = client.SendMessage("这是一条普通通知消息")

	// 发送 Markdown 格式消息
	_, _ = client.SendMarkdownMessage("*重要通知*\n\n这是一条 `Markdown` 格式的消息")

	// 发送 HTML 格式消息
	_, _ = client.SendHTMLMessage("<b>重要通知</b>\n\n这是一条 <code>HTML</code> 格式的消息")
}

// ExampleUsageWithDirectConfig 展示直接传入配置的方式
func ExampleUsageWithDirectConfig() {
	// 方式二：直接传入配置（适用于临时使用或测试）
	client := NewTelegramClient(
		"123456789:ABCdefGHIjklMNOpqrsTUVwxyz", // 替换为你的 Bot Token
		"123456789",                            // 替换为你的 Chat ID
	)

	// 发送消息
	_, _ = client.SendMessage("测试消息")
}

// ExampleUsageWithErrorHandling 展示带错误处理的使用方式
func ExampleUsageWithErrorHandling() {
	// 检查配置是否已设置
	globalCfg := config.GetGlobalConfig()
	if globalCfg == nil || globalCfg.Telegram.BotToken == "" || globalCfg.Telegram.ChatID == "" {
		// Telegram 未配置，跳过通知
		return
	}

	client := NewTelegramClient(
		globalCfg.Telegram.BotToken,
		globalCfg.Telegram.ChatID,
	)

	// 发送消息并处理错误
	if _, err := client.SendMessage("这是一条带错误处理的消息"); err != nil {
		// 记录错误，但不中断主流程
		// logger.GetLoggerInstance().Error("Telegram 通知发送失败", zap.Error(err))
	}
}

// ExampleUsageChangeChatID 展示如何动态更改 Chat ID
func ExampleUsageChangeChatID() {
	globalCfg := config.GetGlobalConfig()
	if globalCfg == nil {
		return
	}
	client := NewTelegramClient(
		globalCfg.Telegram.BotToken,
		globalCfg.Telegram.ChatID,
	)

	// 发送到默认 Chat ID
	_, _ = client.SendMessage("发送到默认 Chat ID")

	// 更改 Chat ID（例如切换到另一个用户或群组）
	client.SetChatID("987654321")

	// 发送到新的 Chat ID
	_, _ = client.SendMessage("发送到新的 Chat ID")
}

// ExampleUsageWithOptions 展示使用配置选项的方式
func ExampleUsageWithOptions() {
	globalCfg := config.GetGlobalConfig()
	if globalCfg == nil {
		return
	}
	botToken := globalCfg.Telegram.BotToken
	chatID := globalCfg.Telegram.ChatID
	
	// 使用默认配置（启用代理）
	client1 := NewTelegramClient(
		botToken,
		chatID,
	)
	_, _ = client1.SendMessage("使用默认配置")

	// 禁用代理
	client2 := NewTelegramClient(
		botToken,
		chatID,
		WithProxy(false),
	)
	_, _ = client2.SendMessage("禁用代理")

	// 使用自定义代理地址
	client3 := NewTelegramClient(
		botToken,
		chatID,
		WithProxyURL("http://proxy.example.com:8080"),
	)
	_, _ = client3.SendMessage("使用自定义代理")

	// 设置自定义超时时间
	client4 := NewTelegramClient(
		botToken,
		chatID,
		WithTimeout(time.Second*30),
	)
	_, _ = client4.SendMessage("设置超时时间为 30 秒")

	// 组合使用多个选项
	client5 := NewTelegramClient(
		botToken,
		chatID,
		WithProxy(false),            // 禁用代理
		WithTimeout(time.Second*60), // 设置超时时间为 60 秒
	)
	_, _ = client5.SendMessage("组合使用多个选项")
}
