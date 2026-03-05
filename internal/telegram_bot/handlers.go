package telegram_bot

import (
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/position"
	"auto-arbitrage/internal/statistics"
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
)

// FormatWalletDetail 格式化钱包详情为 TG 消息（与 home 页列表一致）
func FormatWalletDetail() string {
	wm := position.GetWalletManager()
	if wm == nil {
		return "暂无钱包数据"
	}
	wi := wm.GetWalletInfo()
	if wi == nil {
		return "暂无钱包数据"
	}

	var sb strings.Builder
	sb.WriteString("📋 资产详情\n")
	sb.WriteString("────────────\n")
	sb.WriteString(fmt.Sprintf("💎 总资产: %.2f USDT\n", wi.TotalAsset))
	sb.WriteString(fmt.Sprintf("📈 未实现盈亏: %.2f USDT\n", wi.TotalUnrealizedPnl))
	sb.WriteString(fmt.Sprintf("🔗 链上资产: %.2f USDT\n", wi.TotalOnchainValue))
	sb.WriteString("────────────\n")

	// 交易所列表
	if wi.ExchangeWallets != nil {
		names := make([]string, 0, len(wi.ExchangeWallets))
		for n := range wi.ExchangeWallets {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			w := wi.ExchangeWallets[name]
			if w == nil {
				continue
			}
			bal := w.TotalBalanceValue
			pos := w.TotalPositionValue
			pnl := w.TotalUnrealizedPnl
			equity := bal + pnl
			sb.WriteString(fmt.Sprintf("\n🏦 %s\n", strings.ToUpper(name)))
			sb.WriteString(fmt.Sprintf("  余额: %.2f | 持仓名义: %.2f | 盈亏: %.2f\n", bal, pos, pnl))
			sb.WriteString(fmt.Sprintf("  净资产: %.2f USDT\n", equity))
			if w.Positions != nil && len(w.Positions) > 0 {
				sb.WriteString("  合约持仓:\n")
				for sym, p := range w.Positions {
					if p != nil {
						sb.WriteString(fmt.Sprintf("    %s %s %.4f @ %.2f (盈亏 %.2f)\n",
							sym, p.Side, p.Size, p.MarkPrice, p.UnrealizedPnl))
					}
				}
			}
		}
	}

	// 链上列表
	if wi.OnchainBalances != nil && len(wi.OnchainBalances) > 0 {
		sb.WriteString("\n🔗 链上余额\n")
		chains := make([]string, 0, len(wi.OnchainBalances))
		for c := range wi.OnchainBalances {
			chains = append(chains, c)
		}
		sort.Strings(chains)
		for _, chainID := range chains {
			sm := wi.OnchainBalances[chainID]
			if sm == nil {
				continue
			}
			chainName := chainID
			if chainID == "56" {
				chainName = "BSC"
			} else if chainID == "1" {
				chainName = "ETH"
			}
			sb.WriteString(fmt.Sprintf("  Chain %s: %d tokens\n", chainName, len(sm)))
		}
	}

	return sb.String()
}

// FormatProfitReport 格式化收益报告：近7天数据 + 基准收益计算
// 返回 (文本, 图表PNG字节)。chartPNG 为 nil 时仅发文本
func FormatProfitReport() (text string, chartPNG []byte) {
	sm := statistics.GetStatisticsManager()
	if sm == nil {
		return "暂无统计数据", nil
	}
	end := time.Now().Unix()
	start := time.Now().AddDate(0, 0, -7).Unix()
	history, err := sm.GetAssetHistory(start, end)
	if err != nil || len(history) == 0 {
		return "近7天暂无资产历史数据", nil
	}

	cfg := config.GetGlobalConfig()
	baseline := 10000.0 // 默认基准
	if cfg != nil && cfg.Telegram.BaselineInvestment > 0 {
		baseline = cfg.Telegram.BaselineInvestment
	}

	latest := history[len(history)-1].TotalAsset
	profit := latest - baseline
	profitPct := 0.0
	if baseline > 0 {
		profitPct = profit / baseline * 100
	}

	loc, _ := time.LoadLocation("Asia/Shanghai")
	var sb strings.Builder
	sb.WriteString("📊 收益情况 (近7天)\n")
	sb.WriteString("────────────\n")
	sb.WriteString(fmt.Sprintf("基准投入: %.2f USDT\n", baseline))
	sb.WriteString(fmt.Sprintf("当前总资产: %.2f USDT\n", latest))
	sb.WriteString(fmt.Sprintf("收益: %.2f USDT (%.2f%%)\n", profit, profitPct))
	if profit >= 0 {
		sb.WriteString("✅ 盈利\n")
	} else {
		sb.WriteString("❌ 亏损\n")
	}
	sb.WriteString("────────────\n")
	sb.WriteString("趋势:\n")

	// 采样显示（最多10个点）
	step := 1
	if len(history) > 10 {
		step = len(history) / 10
	}
	for i := 0; i < len(history); i += step {
		s := history[i]
		t := time.Unix(s.Timestamp, 0).In(loc)
		sb.WriteString(fmt.Sprintf("%s %.2f\n", t.Format("01/02 15:04"), s.TotalAsset))
	}
	if (len(history)-1)%step != 0 {
		s := history[len(history)-1]
		t := time.Unix(s.Timestamp, 0).In(loc)
		sb.WriteString(fmt.Sprintf("%s %.2f\n", t.Format("01/02 15:04"), s.TotalAsset))
	}

	// 生成图表
	chartPNG = generateAssetChartPNG(history, baseline)
	return sb.String(), chartPNG
}

func generateAssetChartPNG(history []statistics.AssetSnapshot, baseline float64) []byte {
	if len(history) < 2 {
		return nil
	}
	p := plot.New()
	p.Title.Text = "7日资产趋势"
	p.X.Label.Text = "时间"
	p.Y.Label.Text = "USDT"

	pts := make(plotter.XYs, len(history))
	for i, s := range history {
		pts[i].X = float64(s.Timestamp)
		pts[i].Y = s.TotalAsset
	}
	line, err := plotter.NewLine(pts)
	if err != nil {
		return nil
	}
	line.LineStyle.Width = vg.Points(2)
	p.Add(line)

	// 基准线
	if baseline > 0 {
		basePts := plotter.XYs{{X: float64(history[0].Timestamp), Y: baseline}, {X: float64(history[len(history)-1].Timestamp), Y: baseline}}
		baseLine, err := plotter.NewLine(basePts)
		if err == nil {
			baseLine.LineStyle.Dashes = []vg.Length{vg.Points(4), vg.Points(2)}
			p.Add(baseLine)
		}
	}

	p.X.Tick.Marker = plot.TimeTicks{Format: "01/02 15:04"}
	w := 8 * vg.Inch
	h := 4 * vg.Inch
	wt, err := p.WriterTo(w, h, "png")
	if err != nil {
		return nil
	}
	var buf bytes.Buffer
	if _, err := wt.WriteTo(&buf); err != nil {
		return nil
	}
	return buf.Bytes()
}

// GetBaselineFromConfig 获取基准投入（供外部使用）
func GetBaselineFromConfig() float64 {
	cfg := config.GetGlobalConfig()
	if cfg == nil {
		return 0
	}
	return cfg.Telegram.BaselineInvestment
}
