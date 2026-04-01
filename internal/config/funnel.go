package config

// FunnelConfig 搬砖机会漏斗与监控池参数（可通过 settings.yaml / 环境变量覆盖）
type FunnelConfig struct {
	// 层2：价格斜率加速比（短窗/长窗）下限
	PriceAccelThreshold float64 `mapstructure:"price_accel_threshold"`
	// 层3：挂单量斜率加速比下限
	DepthAccelThreshold float64 `mapstructure:"depth_accel_threshold"`
	// 层4：挂单量「猛增」加速比下限
	VolumeAccelThreshold float64 `mapstructure:"volume_accel_threshold"`
	// 层1：价差相对历史分布的 σ 倍数下限（越大越严）
	AnomalyStdDevK float64 `mapstructure:"anomaly_stddev_k"`
	// 活跃异常后，连续多少轮价差回到 [-1%,1%] 才视为「行情结束」并进入冷却（轮次×拉取间隔≈时长）
	ActiveNormalRounds int `mapstructure:"active_normal_rounds"`
	// 连续多少轮未出现在数据中则从监控池移入冷却
	WatchPoolNotSeenRounds int `mapstructure:"watch_pool_not_seen_rounds"`
	// 至少积累多少轮价差样本后才参与 σ 突变检测
	WatchPoolMinHistory int `mapstructure:"watch_pool_min_history"`
	// 现货第一档买价名义金额下限（USDT，qty×bestBidPrice）；0 表示不限制。用于过滤极薄盘口噪声
	MinBidNotionalUSDT float64 `mapstructure:"min_bid_notional_usdt"`
}

// DefaultFunnelConfig 与当前产品默认一致的漏斗参数（偏敏感，易出机会；可按环境收紧）
func DefaultFunnelConfig() FunnelConfig {
	return FunnelConfig{
		PriceAccelThreshold:    1.0,
		DepthAccelThreshold:    1.0,
		VolumeAccelThreshold:   1.5,
		AnomalyStdDevK:         2.5,
		ActiveNormalRounds:     45,
		WatchPoolNotSeenRounds: 60,
		WatchPoolMinHistory:    5,
		MinBidNotionalUSDT:     500,
	}
}

// mergeFunnel 将 def 与 v 合并，v 中零值沿用 def
func mergeFunnel(v FunnelConfig) FunnelConfig {
	def := DefaultFunnelConfig()
	if v.PriceAccelThreshold > 0 {
		def.PriceAccelThreshold = v.PriceAccelThreshold
	}
	if v.DepthAccelThreshold > 0 {
		def.DepthAccelThreshold = v.DepthAccelThreshold
	}
	if v.VolumeAccelThreshold > 0 {
		def.VolumeAccelThreshold = v.VolumeAccelThreshold
	}
	if v.AnomalyStdDevK > 0 {
		def.AnomalyStdDevK = v.AnomalyStdDevK
	}
	if v.ActiveNormalRounds > 0 {
		def.ActiveNormalRounds = v.ActiveNormalRounds
	}
	if v.WatchPoolNotSeenRounds > 0 {
		def.WatchPoolNotSeenRounds = v.WatchPoolNotSeenRounds
	}
	if v.WatchPoolMinHistory > 0 {
		def.WatchPoolMinHistory = v.WatchPoolMinHistory
	}
	// MinBidNotionalUSDT 在 config.Load 用 viper.IsSet 单独处理，避免未配置时被 0 覆盖默认值
	return def
}
