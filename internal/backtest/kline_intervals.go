package backtest

// KlineGranularity 回测使用的 K 线粒度（各所 REST 能力不同）
type KlineGranularity string

const (
	Granularity1s KlineGranularity = "1s"
	Granularity1m KlineGranularity = "1m"
)

// ExchangeSpotKlineSupport 现货历史 K 是否支持秒级（保守默认，实现拉取时以官方为准）
var ExchangeSpotKlineSupport = map[string]KlineGranularity{
	"binance": Granularity1s,
	"bybit":   Granularity1m,
	"okx":     Granularity1m,
	"gate":    Granularity1m,
	"bitget":  Granularity1m,
}

// ExchangeFuturesKlineSupport U 本位合约历史 K 粒度（保守默认）
var ExchangeFuturesKlineSupport = map[string]KlineGranularity{
	"binance": Granularity1m,
	"bybit":   Granularity1m,
	"okx":     Granularity1m,
	"gate":    Granularity1m,
	"bitget":  Granularity1m,
}

// WarningsForPair 粒度提示
func WarningsForPair(spotEx, futEx string) []string {
	var w []string
	ss := exchangeSpotOr1m(spotEx)
	fs := exchangeFutOr1m(futEx)
	if ss != fs {
		w = append(w, "现货与合约历史 K 线粒度不一致，已按时间对齐到较粗粒度")
	}
	if ss == Granularity1m || fs == Granularity1m {
		w = append(w, "部分交易所仅支持 1m K 线，回测为分钟级步进，与线上秒级 ticker 存在差异")
	}
	return w
}

func exchangeSpotOr1m(ex string) KlineGranularity {
	if g, ok := ExchangeSpotKlineSupport[ex]; ok {
		return g
	}
	return Granularity1m
}

func exchangeFutOr1m(ex string) KlineGranularity {
	if g, ok := ExchangeFuturesKlineSupport[ex]; ok {
		return g
	}
	return Granularity1m
}
