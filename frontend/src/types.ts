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
}

export interface OpportunityItem {
  symbol: string
  spot_exchange: string
  futures_exchange: string
  spread_percent: number
  spot_orderbook_depth: number
  futures_orderbook_depth: number
  price_slope_5m: number
  volume_spike: boolean
  confidence: number
  updated_at: string
}

export interface FunnelStats {
  total_symbols: number
  after_negative_spread: number
  after_spot_depth: number
  after_price_slope: number
  after_volume: number
  after_both_depth: number
}

export interface OpportunitiesResponse {
  opportunities: OpportunityItem[]
  funnel_stats: FunnelStats
  updated_at: string
}
