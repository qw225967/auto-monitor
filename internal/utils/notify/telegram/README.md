# Telegram 机器人通知功能使用指南

## 如何获取 NewTelegramClient 的入参

`NewTelegramClient` 函数使用函数式选项模式，需要两个必需参数和可选的配置选项：
1. **botToken** - Telegram Bot Token（必需）
2. **chatID** - 接收消息的 Chat ID（必需）
3. **opts** - 可选的配置选项（使用 `WithProxy`, `WithProxyURL`, `WithTimeout` 等）

### 配置选项说明

- `WithProxy(useProxy bool)` - 设置是否启用代理（默认启用）
- `WithProxyURL(proxyURL string)` - 设置代理地址（设置后自动启用代理）
- `WithTimeout(timeout time.Duration)` - 设置 HTTP 请求超时时间（默认 10 秒）

### 1. 获取 Bot Token

#### 步骤：
1. 在 Telegram 中搜索并打开 **@BotFather**
2. 发送 `/newbot` 命令创建新机器人
3. 按照提示设置机器人名称和用户名
4. BotFather 会返回一个 Bot Token，格式类似：`123456789:ABCdefGHIjklMNOpqrsTUVwxyz`
5. **保存这个 Token**，这是你的 `botToken` 参数

#### 示例对话：
```
你: /newbot
BotFather: Alright, a new bot. How are we going to call it? Please choose a name for your bot.
你: My Notification Bot
BotFather: Good. Now let's choose a username for your bot. It must end in `bot`. Like this, for example: TetrisBot or tetris_bot.
你: my_notification_bot
BotFather: Done! Congratulations on your new bot. You will find it at t.me/my_notification_bot. Use this token to access the HTTP API:
123456789:ABCdefGHIjklMNOpqrsTUVwxyz
```

### 2. 获取 Chat ID

Chat ID 可以是：
- **个人用户 ID**：接收消息的 Telegram 用户 ID
- **群组 ID**：接收消息的 Telegram 群组 ID

#### 方法一：获取个人用户 ID（推荐用于个人通知）

1. 在 Telegram 中搜索并打开 **@userinfobot**
2. 发送任意消息给这个机器人
3. 它会返回你的用户 ID，这就是你的 `chatID` 参数

#### 方法二：获取群组 ID（推荐用于群组通知）

1. 创建一个 Telegram 群组
2. 将你的机器人添加到群组中（在群组中搜索你的机器人用户名并添加）
3. 在群组中发送一条消息
4. 访问以下 URL（将 `YOUR_BOT_TOKEN` 替换为你的 Bot Token）：
   ```
   https://api.telegram.org/botYOUR_BOT_TOKEN/getUpdates
   ```
5. 在返回的 JSON 中查找 `"chat":{"id":-123456789}`，这个数字就是群组的 `chatID`
   - 注意：群组 ID 通常是负数（如 `-123456789`）

#### 方法三：使用临时机器人获取 Chat ID

1. 创建一个简单的测试脚本或使用以下命令：
   ```bash
   curl https://api.telegram.org/botYOUR_BOT_TOKEN/getUpdates
   ```
2. 向你的机器人发送一条消息（私聊或群组）
3. 再次运行上面的命令，在返回的 JSON 中找到 `chat.id` 字段

### 3. 配置到项目中

#### 方式一：直接在 config.go 中配置（当前项目风格）

编辑 `internal/config/config.go` 文件，填入你的配置：

```go
const (
	TelegramBotToken = "123456789:ABCdefGHIjklMNOpqrsTUVwxyz" // 你的 Bot Token
	TelegramChatID   = "123456789"                            // 你的 Chat ID
)
```

#### 方式二：在代码中直接使用

```go
import "auto-arbitrage/internal/utils/notify/telegram"

// 创建客户端
client := telegram.NewTelegramClient(
	"123456789:ABCdefGHIjklMNOpqrsTUVwxyz", // botToken
	"123456789",                             // chatID
)
```

#### 方式三：从配置文件读取（推荐用于生产环境）

```go
import (
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/utils/notify/telegram"
)

// 从配置中读取
client := telegram.NewTelegramClient(
	config.TelegramBotToken,
	config.TelegramChatID,
)
```

## 使用示例

### 基本使用

```go
package main

import (
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/utils/notify/telegram"
	"auto-arbitrage/internal/utils/logger"
)

func main() {
	// 创建 Telegram 客户端
	client := telegram.NewTelegramClient(
		config.TelegramBotToken,
		config.TelegramChatID,
	)

	// 发送普通文本消息
	msgID, err := client.SendMessage("这是一条测试消息")
	if err != nil {
		logger.GetLoggerInstance().Error("发送消息失败", zap.Error(err))
		return
	}

	// 编辑消息
	_, err = client.EditMessageText(msgID, "这是一条被编辑过的测试消息")
	if err != nil {
		logger.GetLoggerInstance().Error("编辑消息失败", zap.Error(err))
	}

	// 发送 Markdown 格式消息
	_, err = client.SendMarkdownMessage("*这是粗体* 和 `代码`")
	if err != nil {
		logger.GetLoggerInstance().Error("发送消息失败", zap.Error(err))
		return
	}

	// 发送 HTML 格式消息
	_, err = client.SendHTMLMessage("<b>这是粗体</b> 和 <code>代码</code>")
	if err != nil {
		logger.GetLoggerInstance().Error("发送消息失败", zap.Error(err))
		return
	}
}
```

### 在项目中使用（从配置读取）

```go
import (
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/utils/notify/telegram"
)

// 初始化通知客户端
func initTelegramNotifier() *telegram.TelegramClient {
	if config.TelegramBotToken == "" || config.TelegramChatID == "" {
		return nil // 未配置 Telegram
	}
	return telegram.NewTelegramClient(config.TelegramBotToken, config.TelegramChatID)
}

// 发送通知
func sendNotification(message string) {
	client := initTelegramNotifier()
	if client == nil {
		return // Telegram 未配置，跳过通知
	}
	
	if _, err := client.SendMessage(message); err != nil {
		// 记录错误但不中断主流程
		logger.GetLoggerInstance().Error("Telegram 通知发送失败", zap.Error(err))
	}
}
```

### 编辑已发送的消息

你可以使用 `EditMessageText` 方法来编辑已经发送的消息。这在需要更新状态或修正信息时非常有用。

```go
// 发送一条消息
msgID, err := client.SendMessage("正在处理...")
if err != nil {
    // 处理错误
}

// 执行一些操作...

// 更新消息内容
_, err = client.EditMessageText(msgID, "✅ 处理完成！")
if err != nil {
    // 处理错误（例如消息太旧无法编辑）
}
```

### 使用配置选项

```go
import (
	"time"
	"auto-arbitrage/internal/utils/notify/telegram"
)

// 使用默认配置（启用代理，使用默认代理地址和超时时间）
client := telegram.NewTelegramClient(token, chatID)

// 禁用代理
client := telegram.NewTelegramClient(token, chatID, telegram.WithProxy(false))

// 使用自定义代理地址
client := telegram.NewTelegramClient(token, chatID, telegram.WithProxyURL("http://proxy.example.com:8080"))

// 设置自定义超时时间
client := telegram.NewTelegramClient(token, chatID, telegram.WithTimeout(time.Second*30))

// 组合使用多个选项
client := telegram.NewTelegramClient(
	token, 
	chatID,
	telegram.WithProxy(false),                    // 禁用代理
	telegram.WithTimeout(time.Second*60),        // 设置超时时间为 60 秒
)
```

## 注意事项

1. **安全性**：Bot Token 是敏感信息，不要提交到公开的代码仓库
2. **Chat ID 格式**：
   - 个人用户 ID：通常是正数（如 `123456789`）
   - 群组 ID：通常是负数（如 `-123456789`）
3. **消息限制**：Telegram Bot API 对消息发送频率有限制，避免过于频繁地发送消息
4. **代理配置**：
   - 默认情况下会启用代理（`http://127.0.0.1:9876`）
   - 可以使用配置选项灵活控制代理和超时设置：
     ```go
     // 使用默认配置（启用代理）
     client := telegram.NewTelegramClient(token, chatID)
     
     // 禁用代理
     client := telegram.NewTelegramClient(token, chatID, telegram.WithProxy(false))
     
     // 使用自定义代理地址
     client := telegram.NewTelegramClient(token, chatID, telegram.WithProxyURL("http://proxy:8080"))
     
     // 设置超时时间
     client := telegram.NewTelegramClient(token, chatID, telegram.WithTimeout(time.Second*30))
     ```

## 常见问题

### Q: 如何测试 Bot Token 是否有效？
A: 访问 `https://api.telegram.org/botYOUR_BOT_TOKEN/getMe`，如果返回包含 `"ok":true`，说明 Token 有效。

### Q: 如何知道消息是否发送成功？
A: `SendMessage` 等方法会返回 `messageID` 和 `error`。如果 `error` 为 `nil` 则表示发送成功，`messageID` 可用于后续编辑消息。

### Q: 可以发送图片或文件吗？
A: 当前实现只支持文本消息。如需发送图片或文件，可以扩展 `TelegramClient` 添加相应方法。

### Q: Chat ID 会变化吗？
A: 个人用户 ID 不会变化，但群组 ID 可能会在某些情况下变化（如群组被删除重建）。




