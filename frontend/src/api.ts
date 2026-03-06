import type { OverviewResponse } from './types'

const API_BASE = '/api'

export async function fetchOverview(): Promise<OverviewResponse> {
  const res = await fetch(`${API_BASE}/overview`)
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`)
  }
  return res.json()
}

/** 提交交易所密钥（仅存后端内存，不落盘） */
export async function postExchangeKeys(keysJson: string): Promise<{ ok: boolean; message?: string }> {
  const res = await fetch(`${API_BASE}/config/exchange-keys`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ keys: keysJson }),
  })
  const data = await res.json()
  if (!res.ok) {
    throw new Error(data.error || data.message || '提交失败')
  }
  return data
}

/** 设置流动性阈值（USDT），低于该阈值的链上套利不展示 */
export async function postLiquidityThreshold(threshold: number): Promise<{ ok: boolean; message?: string; threshold?: number }> {
  const res = await fetch(`${API_BASE}/config/liquidity-threshold`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ threshold }),
  })
  const data = await res.json()
  if (!res.ok) {
    throw new Error(data.error || data.message || '提交失败')
  }
  return data
}
