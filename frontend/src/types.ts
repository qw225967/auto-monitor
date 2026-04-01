export interface DetailPathRow {
  path_id: string
  physical_flow: string
  status: string
}

export interface OverviewRow {
  type?: string // cex_cex | cex_dex | dex_dex
  symbol: string
  path_display: string
  chain_liquidity?: string // 链流动性，如 "ETH: 100万"
  buy_exchange: string
  sell_exchange: string
  spread_percent: number
  gross_spread_percent?: number
  estimated_cost_percent?: number
  net_spread_percent?: number
  confidence_score?: number
  available_path_count?: number
  detail_paths?: DetailPathRow[]
}

export const OppTypeLabels: Record<string, string> = {
  cex_cex: '交易所-交易所',
  cex_dex: '交易所-链',
  dex_dex: '链-链',
}

export interface OverviewResponse {
  overview: OverviewRow[]
  /** 当前生效的流动性阈值（USDT），0 表示不限制 */
  liquidity_threshold?: number
  overview_updated_at?: string
  chain_prices_updated_at?: string
  liquidity_updated_at?: string
  overview_age_sec?: number
  chain_prices_age_sec?: number
  liquidity_age_sec?: number
  /** 最近一次通路探测失败时的错误信息，空表示成功 */
  last_detect_error?: string
}

export interface OpportunityItem {
  symbol: string
  spot_exchange: string
  futures_exchange: string
  spread_percent: number
  spot_orderbook_depth: number
  /** 价差相对历史均值的 σ 倍数（突变强度） */
  spread_anomaly?: number
  /** 价格斜率加速比（短窗/长窗） */
  price_accel_ratio?: number
  /** 挂单量斜率加速比（层4 阈值判定用） */
  volume_accel_score?: number
  confidence: number
  updated_at: string
}

export interface FunnelStats {
  /** 本轮 API 返回的价差条目总数 */
  total_symbols: number
  /** 本轮价差在 [-1%, 1%] 且进入监控池统计的条数（旧版后端可能缺失） */
  after_spread_in_range?: number
  /** 当前监控池内 symbol 数量 */
  watch_pool_size?: number
  /** 冷却列表中的 symbol 数 */
  cooling_pool_size?: number
  /** 层1：价差突变（2σ） */
  after_spread_anomaly: number
  /** 层2+3：价格斜率 + 挂单量斜率加速 */
  after_price_accel: number
  /** 层4：挂单量猛增 → 最终机会数 */
  after_depth_volume: number
}

export interface OpportunitiesResponse {
  opportunities: OpportunityItem[]
  funnel_stats: FunnelStats
  updated_at: string
}
