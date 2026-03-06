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
}
