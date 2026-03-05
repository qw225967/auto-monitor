package telegram

import (
	"auto-arbitrage/internal/config"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var GlobalTgBotClient *TelegramClient

const (
	// TelegramBotAPIBaseURL Telegram Bot API 基础地址
	TelegramBotAPIBaseURL = "https://api.telegram.org/bot"
	// DefaultTimeout 默认超时时间（秒）
	DefaultTimeout = 10
)

// clientOptions 客户端配置选项
type clientOptions struct {
	useProxyConfig bool          // 是否使用统一的代理配置管理器（默认 true）
	customProxyURL string        // 自定义代理地址（如果设置，会通过 proxy_config 设置）
	timeout        time.Duration // 超时时间
}

// Option 配置选项函数类型
type Option func(*clientOptions)

// TelegramClient Telegram 机器人客户端
type TelegramClient struct {
	botToken         string
	chatID           string
	httpClient       *http.Client
	longPollClient   *http.Client // 用于 getUpdates 长轮询，超时需大于 Telegram 的 timeout 参数
}

// WithProxy 设置是否启用代理
// useProxy: true 启用代理（使用统一的代理配置管理器），false 禁用代理
// 注意：此选项已集成到统一的代理配置管理器中，代理配置会从环境变量或 proxy_config 读取
func WithProxy(useProxy bool) Option {
	return func(opts *clientOptions) {
		opts.useProxyConfig = useProxy
	}
}

// WithProxyURL 设置代理地址
// proxyURL: 代理地址，例如 "http://127.0.0.1:9876"
// 注意：此选项会通过统一的代理配置管理器设置代理，设置后会自动启用代理
func WithProxyURL(proxyURL string) Option {
	return func(opts *clientOptions) {
		opts.customProxyURL = proxyURL
		opts.useProxyConfig = true // 设置代理地址时自动启用代理
	}
}

// WithTimeout 设置 HTTP 请求超时时间
// timeout: 超时时间，例如 time.Second * 30
func WithTimeout(timeout time.Duration) Option {
	return func(opts *clientOptions) {
		opts.timeout = timeout
	}
}

// NewTelegramClient 创建新的 Telegram 客户端
// botToken: Telegram Bot Token (从 @BotFather 获取)
// chatID: 接收消息的 Chat ID (用户ID或群组ID)
// opts: 可选的配置选项，例如 WithProxy(false), WithProxyURL("http://proxy:8080"), WithTimeout(time.Second*30)
// 注意：默认使用统一的代理配置管理器（从环境变量 HTTP_PROXY/HTTPS_PROXY/PROXY_URL 读取）
func NewTelegramClient(botToken, chatID string, opts ...Option) *TelegramClient {
	// 设置默认选项
	options := &clientOptions{
		useProxyConfig: true,                                        // 默认使用统一的代理配置管理器
		customProxyURL: "",                                          // 默认不设置自定义代理地址
		timeout:        time.Duration(DefaultTimeout) * time.Second, // 默认超时时间
	}

	// 应用用户提供的选项
	for _, opt := range opts {
		opt(options)
	}

	// 确定超时时间
	timeout := options.timeout
	if timeout == 0 {
		timeout = time.Duration(DefaultTimeout) * time.Second
	}

	// 创建 HTTP Client 与长轮询用 Client（getUpdates 需 40s 超时）
	longPollTimeout := 40 * time.Second
	var httpClient, longPollClient *http.Client
	if options.useProxyConfig {
		proxyConfig := config.GetProxyConfig()
		if options.customProxyURL != "" {
			_ = proxyConfig.SetProxyURL(options.customProxyURL)
		}
		httpClient = proxyConfig.CreateClient(timeout)
		longPollClient = proxyConfig.CreateClient(longPollTimeout)
	} else {
		httpClient = &http.Client{Timeout: timeout}
		longPollClient = &http.Client{Timeout: longPollTimeout}
	}

	return &TelegramClient{
		botToken:       botToken,
		chatID:         chatID,
		httpClient:     httpClient,
		longPollClient: longPollClient,
	}
}

func (tc *TelegramClient) genBaseUrl() string {
	return TelegramBotAPIBaseURL + tc.botToken
}

// SendMessage 发送普通文本消息
// message: 要发送的消息内容
// 返回: messageID, error
func (tc *TelegramClient) SendMessage(message string) (int, error) {
	return tc.SendMessageWithParseMode(message, "")
}

// SendMarkdownMessage 发送 Markdown 格式消息
// message: 要发送的消息内容（支持 Markdown 格式）
// 返回: messageID, error
func (tc *TelegramClient) SendMarkdownMessage(message string) (int, error) {
	return tc.SendMessageWithParseMode(message, "Markdown")
}

// SendHTMLMessage 发送 HTML 格式消息
// message: 要发送的消息内容（支持 HTML 格式）
// 返回: messageID, error
func (tc *TelegramClient) SendHTMLMessage(message string) (int, error) {
	return tc.SendMessageWithParseMode(message, "HTML")
}

// SendMessageRequest 发送消息请求结构
type SendMessageRequest struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"` // "Markdown" 或 "HTML"
}

// EditMessageTextRequest 编辑消息请求结构
type EditMessageTextRequest struct {
	ChatID    string `json:"chat_id"`
	MessageID int    `json:"message_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

// SendMessageResponse Telegram API 响应结构
type SendMessageResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
	ErrorCode   int    `json:"error_code,omitempty"`
	Result      struct {
		MessageID int `json:"message_id"`
	} `json:"result,omitempty"`
}

// getUpdates 相关结构（Telegram Bot API）
type tgChat struct {
	ID int64 `json:"id"`
}

type tgMessage struct {
	MessageID int     `json:"message_id"`
	Chat      *tgChat `json:"chat"`
	Text      string  `json:"text"`
}

type tgUpdate struct {
	UpdateID      int         `json:"update_id"`
	Message       *tgMessage  `json:"message"`
	EditedMessage *tgMessage  `json:"edited_message"`
}

type getUpdatesResponse struct {
	OK     bool        `json:"ok"`
	Result []tgUpdate  `json:"result"`
}

// SendMessageToChat 向指定 chatID 发送纯文本消息（用于回复「查询资产」等）
func (tc *TelegramClient) SendMessageToChat(chatID string, message string) (int, error) {
	requestBody := SendMessageRequest{
		ChatID: chatID,
		Text:   message,
	}
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return 0, fmt.Errorf("序列化请求数据失败: %w", err)
	}
	apiURL := tc.genBaseUrl() + "/sendMessage"
	request, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return 0, fmt.Errorf("创建请求失败: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := tc.httpClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("发送请求失败: %w", err)
	}
	defer response.Body.Close()
	bodyStr, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return 0, fmt.Errorf("读取响应失败: %w", err)
	}
	var apiResponse SendMessageResponse
	if err := json.Unmarshal(bodyStr, &apiResponse); err != nil {
		return 0, fmt.Errorf("解析响应失败: %w", err)
	}
	if !apiResponse.OK {
		return 0, fmt.Errorf("Telegram API 错误: %s (错误代码: %d)", apiResponse.Description, apiResponse.ErrorCode)
	}
	return apiResponse.Result.MessageID, nil
}

// SendPhotoToChat 向指定 chatID 发送图片（PNG 字节流），caption 为可选说明文字
func (tc *TelegramClient) SendPhotoToChat(chatID string, pngData []byte, caption string) (int, error) {
	if len(pngData) == 0 {
		return 0, fmt.Errorf("图片数据为空")
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", chatID)
	if caption != "" {
		_ = w.WriteField("caption", caption)
	}
	part, err := w.CreateFormFile("photo", "chart.png")
	if err != nil {
		return 0, fmt.Errorf("创建表单文件失败: %w", err)
	}
	if _, err := part.Write(pngData); err != nil {
		return 0, fmt.Errorf("写入图片数据失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return 0, fmt.Errorf("关闭 multipart 失败: %w", err)
	}
	apiURL := tc.genBaseUrl() + "/sendPhoto"
	request, err := http.NewRequest("POST", apiURL, &buf)
	if err != nil {
		return 0, fmt.Errorf("创建请求失败: %w", err)
	}
	request.Header.Set("Content-Type", w.FormDataContentType())
	response, err := tc.httpClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("发送请求失败: %w", err)
	}
	defer response.Body.Close()
	bodyStr, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return 0, fmt.Errorf("读取响应失败: %w", err)
	}
	var apiResponse SendMessageResponse
	if err := json.Unmarshal(bodyStr, &apiResponse); err != nil {
		return 0, fmt.Errorf("解析响应失败: %w", err)
	}
	if !apiResponse.OK {
		return 0, fmt.Errorf("Telegram API 错误: %s (错误代码: %d)", apiResponse.Description, apiResponse.ErrorCode)
	}
	return apiResponse.Result.MessageID, nil
}

// SendMessageWithParseMode 发送消息（支持自定义解析模式）
// message: 要发送的消息内容
// parseMode: 解析模式，可选值: "Markdown", "HTML" 或空字符串（纯文本）
// 返回: messageID, error
func (tc *TelegramClient) SendMessageWithParseMode(message, parseMode string) (int, error) {
	requestBody := SendMessageRequest{
		ChatID: tc.chatID,
		Text:   message,
	}

	if parseMode != "" {
		requestBody.ParseMode = parseMode
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return 0, fmt.Errorf("序列化请求数据失败: %w", err)
	}

	apiURL := tc.genBaseUrl() + "/sendMessage"
	request, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return 0, fmt.Errorf("创建请求失败: %w", err)
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := tc.httpClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("发送请求失败: %w", err)
	}
	defer response.Body.Close()

	bodyStr, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return 0, fmt.Errorf("读取响应失败: %w", err)
	}

	var apiResponse SendMessageResponse
	if err := json.Unmarshal(bodyStr, &apiResponse); err != nil {
		return 0, fmt.Errorf("解析响应失败: %w", err)
	}

	if !apiResponse.OK {
		return 0, fmt.Errorf("Telegram API 错误: %s (错误代码: %d)", apiResponse.Description, apiResponse.ErrorCode)
	}

	return apiResponse.Result.MessageID, nil
}

// EditMessageText 编辑已发送的消息
// messageID: 要编辑的消息 ID
// text: 新的消息内容
// 返回: messageID, error
func (tc *TelegramClient) EditMessageText(messageID int, text string) (int, error) {
	return tc.EditMessageTextWithParseMode(messageID, text, "")
}

// EditMessageTextWithParseMode 编辑已发送的消息（支持自定义解析模式）
// messageID: 要编辑的消息 ID
// text: 新的消息内容
// parseMode: 解析模式，可选值: "Markdown", "HTML" 或空字符串（纯文本）
// 返回: messageID, error
func (tc *TelegramClient) EditMessageTextWithParseMode(messageID int, text, parseMode string) (int, error) {
	requestBody := EditMessageTextRequest{
		ChatID:    tc.chatID,
		MessageID: messageID,
		Text:      text,
	}

	if parseMode != "" {
		requestBody.ParseMode = parseMode
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return 0, fmt.Errorf("序列化请求数据失败: %w", err)
	}

	apiURL := tc.genBaseUrl() + "/editMessageText"
	request, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return 0, fmt.Errorf("创建请求失败: %w", err)
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := tc.httpClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("发送请求失败: %w", err)
	}
	defer response.Body.Close()

	bodyStr, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return 0, fmt.Errorf("读取响应失败: %w", err)
	}

	var apiResponse SendMessageResponse
	if err := json.Unmarshal(bodyStr, &apiResponse); err != nil {
		return 0, fmt.Errorf("解析响应失败: %w", err)
	}

	if !apiResponse.OK {
		return 0, fmt.Errorf("Telegram API 错误: %s (错误代码: %d)", apiResponse.Description, apiResponse.ErrorCode)
	}

	return apiResponse.Result.MessageID, nil
}

// SetChatID 设置 Chat ID
func (tc *TelegramClient) SetChatID(chatID string) {
	tc.chatID = chatID
}

// GetChatID 获取当前 Chat ID
func (tc *TelegramClient) GetChatID() string {
	return tc.chatID
}

// SetBotToken 设置 Bot Token
func (tc *TelegramClient) SetBotToken(botToken string) {
	tc.botToken = botToken
}

// UpdateFromGlobalConfig 从全局配置更新客户端设置
func UpdateFromGlobalConfig() {
	if GlobalTgBotClient == nil {
		return
	}
	tgConfig := config.GetGlobalConfig().Telegram
	GlobalTgBotClient.SetChatID(tgConfig.ChatID)
	GlobalTgBotClient.SetBotToken(tgConfig.BotToken)
	notifyMsg := "【自动套利系统】🔄 Global Telegram 客户端配置已更新为当前对话"
	GlobalTgBotClient.SendMessage(notifyMsg)
}

// 份额常量：苏 1450，酸 2900，狗 5650，总份额 10000
const (
	shareSu   = 1450.0
	shareSuan = 2900.0
	shareGou  = 5650.0
	shareTotal = shareSu + shareSuan + shareGou
)

func formatAssetReply(totalAsset float64) string {
	if totalAsset <= 0 {
		return "总资产：暂无数据\n苏：初始资产 1450  当前资产 -\n酸：初始资产 2900  当前资产 -\n狗：初始资产 5650  当前资产 -"
	}
	curSu := totalAsset * (shareSu / shareTotal)
	curSuan := totalAsset * (shareSuan / shareTotal)
	curGou := totalAsset * (shareGou / shareTotal)
	return fmt.Sprintf("总资产：%.2f\n苏：初始资产 1450  当前资产 %.2f\n酸：初始资产 2900  当前资产 %.2f\n狗：初始资产 5650  当前资产 %.2f",
		totalAsset, curSu, curSuan, curGou)
}

// getUpdates 调用 getUpdates，timeout 为长轮询秒数，返回本次收到的 updates 与下一轮应使用的 offset
func (tc *TelegramClient) getUpdates(offset int64, timeoutSec int) (updates []tgUpdate, nextOffset int64, err error) {
	apiURL := tc.genBaseUrl() + "/getUpdates?offset=" + strconv.FormatInt(offset, 10) + "&timeout=" + strconv.Itoa(timeoutSec) + "&allowed_updates=[\"message\"]"
	request, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, offset, err
	}
	response, err := tc.longPollClient.Do(request)
	if err != nil {
		return nil, offset, err
	}
	defer response.Body.Close()
	bodyStr, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, offset, err
	}
	var resp getUpdatesResponse
	if err := json.Unmarshal(bodyStr, &resp); err != nil {
		return nil, offset, err
	}
	if !resp.OK {
		return nil, offset, fmt.Errorf("getUpdates not ok: %s", string(bodyStr))
	}
	nextOffset = offset
	for _, u := range resp.Result {
		nextOffset = int64(u.UpdateID) + 1
		updates = append(updates, u)
	}
	return updates, nextOffset, nil
}

// AssetQueryHandlers 资产查询命令处理器，由调用方注入
type AssetQueryHandlers struct {
	GetTotalAsset      func() float64              // 用于「查询资产」
	FormatWalletDetail func() string              // 用于「查询资产详情」
	FormatProfitReport func() (text string, chart []byte) // 用于「查询收益情况」
}

// RunAssetQueryLoop 在后台长轮询 getUpdates，处理资产相关命令。
// handlers 为 nil 时仅支持「查询资产」且总资产按 0 处理；可传入 GetTotalAsset 等回调。
// BotToken 为空时不执行。
func RunAssetQueryLoop(handlers *AssetQueryHandlers) {
	if GlobalTgBotClient == nil || GlobalTgBotClient.botToken == "" {
		return
	}
	var offset int64
	for {
		updates, next, err := GlobalTgBotClient.getUpdates(offset, 30)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}
		offset = next
		for _, u := range updates {
			msg := u.Message
			if msg == nil {
				msg = u.EditedMessage
			}
			if msg == nil || msg.Chat == nil {
				continue
			}
			text := strings.TrimSpace(msg.Text)
			if text == "" {
				continue
			}
			chatID := strconv.FormatInt(msg.Chat.ID, 10)

			if strings.Contains(text, "查询资产详情") && handlers != nil && handlers.FormatWalletDetail != nil {
				reply := handlers.FormatWalletDetail()
				_, _ = GlobalTgBotClient.SendMessageToChat(chatID, reply)
				continue
			}
			if strings.Contains(text, "查询收益情况") && handlers != nil && handlers.FormatProfitReport != nil {
				txt, chart := handlers.FormatProfitReport()
				_, _ = GlobalTgBotClient.SendMessageToChat(chatID, txt)
				if len(chart) > 0 {
					_, _ = GlobalTgBotClient.SendPhotoToChat(chatID, chart, "7日资产趋势")
				}
				continue
			}
			if strings.Contains(text, "查询资产") {
				var total float64
				if handlers != nil && handlers.GetTotalAsset != nil {
					total = handlers.GetTotalAsset()
				}
				reply := formatAssetReply(total)
				if _, sendErr := GlobalTgBotClient.SendMessageToChat(chatID, reply); sendErr != nil {
					_ = sendErr
				}
			}
		}
	}
}
