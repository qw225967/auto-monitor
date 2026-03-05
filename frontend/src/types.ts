export interface DetailPathRow {
  path_id: string
  physical_flow: string
  status: string
}

export interface OverviewRow {
  symbol: string
  path_display: string
  buy_exchange: string
  sell_exchange: string
  spread_percent: number
  available_path_count: number
  detail_paths: DetailPathRow[]
}

export interface OverviewResponse {
  overview: OverviewRow[]
}
