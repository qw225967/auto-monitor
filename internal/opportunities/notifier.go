package opportunities

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/internal/model"
	tg "github.com/qw225967/auto-monitor/internal/utils/notify/telegram"
)

// OpportunityNotifier 机会发现通知器
type OpportunityNotifier struct {
	mu               sync.RWMutex
	tgClient         *tg.TelegramClient
	activeOpps       map[string]*OpportunityState // key: "symbol:spotEx:futuresEx"
	disappearTimeout time.Duration                 // 消失超时时间（默认1分钟）
	lastNotifyTime   time.Time
	minNotifyInterval time.Duration                 // 最小通知间隔
}

// OpportunityState 机会状态
type OpportunityState struct {
	Opportunity model.OpportunityItem
	FirstSeen   time.Time
	LastSeen    time.Time
	Notified    bool // 是否已发送"出现"通知
}

// NewOpportunityNotifier 创建机会通知器
func NewOpportunityNotifier(tgClient *tg.TelegramClient) *OpportunityNotifier {
	return &OpportunityNotifier{
		tgClient:         tgClient,
		activeOpps:       make(map[string]*OpportunityState),
		disappearTimeout: 1 * time.Minute,
		minNotifyInterval: 10 * time.Second,
		lastNotifyTime:   time.Time{},
	}
}

// key 生成机会的唯一key
func (n *OpportunityNotifier) key(opp model.OpportunityItem) string {
	return fmt.Sprintf("%s:%s:%s", opp.Symbol, opp.SpotExchange, opp.FuturesExchange)
}

// Notify 接收新的机会列表，检测出现和消失
func (n *OpportunityNotifier) Notify(opps []model.OpportunityItem) {
	if n.tgClient == nil {
		return
	}

	now := time.Now()
	n.mu.Lock()
	defer n.mu.Unlock()

	// 构建当前机会集合
	currentOppSet := make(map[string]bool)
	for _, opp := range opps {
		k := n.key(opp)
		currentOppSet[k] = true

		// 检查是否新出现的机会
		if state, exists := n.activeOpps[k]; !exists {
			// 新机会出现
			state := &OpportunityState{
				Opportunity: opp,
				FirstSeen:   now,
				LastSeen:    now,
				Notified:    false,
			}
			n.activeOpps[k] = state

			// 立即发送出现通知
			n.sendAppearNotification(state)
			state.Notified = true

			log.Printf("[OpportunityNotifier] 新机会出现: %s %s-%s 价差: %.2f%%", 
				opp.Symbol, opp.SpotExchange, opp.FuturesExchange, opp.SpreadPercent)
		} else {
			// 更新已存在的机会
			state.LastSeen = now
			state.Opportunity = opp
		}
	}

	// 检查消失的机会
	for k, state := range n.activeOpps {
		if !currentOppSet[k] {
			// 机会消失了，检查是否超过超时时间
			if now.Sub(state.LastSeen) >= n.disappearTimeout {
				// 发送消失通知
				n.sendDisappearNotification(state)
				delete(n.activeOpps, k)
			}
		}
	}
}

// sendAppearNotification 发送机会出现通知
func (n *OpportunityNotifier) sendAppearNotification(state *OpportunityState) {
	opp := state.Opportunity
	message := fmt.Sprintf(`🔥 <b>新机会出现</b>

💰 币种: <b>%s</b>
📈 现货交易所: %s
📉 合约交易所: %s
💵 价差: <b>%.2f%%</b>
📊 现货深度: %.2f USDT
📊 合约深度: %.2f USDT
📐 5分钟斜率: %.4f%%
🔊 量能放大: %s
⭐ 置信度: %d%%

⏰ 发现时间: %s`,
		opp.Symbol,
		opp.SpotExchange,
		opp.FuturesExchange,
		opp.SpreadPercent,
		opp.SpotOrderbookDepth,
		opp.FuturesOrderbookDepth,
		opp.PriceSlope5m * 100,
		boolToEmoji(opp.VolumeSpike),
		opp.Confidence,
		opp.UpdatedAt,
	)

	if _, err := n.tgClient.SendHTMLMessage(message); err != nil {
		log.Printf("[OpportunityNotifier] 发送出现通知失败: %v", err)
	} else {
		log.Printf("[OpportunityNotifier] 已发送机会出现通知: %s", opp.Symbol)
	}
}

// sendDisappearNotification 发送机会消失通知
func (n *OpportunityNotifier) sendDisappearNotification(state *OpportunityState) {
	opp := state.Opportunity
	duration := state.LastSeen.Sub(state.FirstSeen)
	message := fmt.Sprintf(`✅ <b>机会结束</b>

💰 币种: <b>%s</b>
📈 现货交易所: %s
📉 合约交易所: %s
💵 结束前价差: <b>%.2f%%</b>

⏰ 持续时间: %.1f 秒
🕐 结束时间: %s`,
		opp.Symbol,
		opp.SpotExchange,
		opp.FuturesExchange,
		opp.SpreadPercent,
		duration.Seconds(),
		time.Now().Format("2006-01-02 15:04:05"),
	)

	if _, err := n.tgClient.SendHTMLMessage(message); err != nil {
		log.Printf("[OpportunityNotifier] 发送消失通知失败: %v", err)
	} else {
		log.Printf("[OpportunityNotifier] 已发送机会消失通知: %s", opp.Symbol)
	}
}

// boolToEmoji 布尔值转emoji
func boolToEmoji(b bool) string {
	if b {
		return "是 🔥"
	}
	return "否"
}
