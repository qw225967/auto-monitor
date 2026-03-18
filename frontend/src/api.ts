import type { OverviewResponse, OpportunitiesResponse } from './types'

const API_BASE = '/api'
const FETCH_TIMEOUT_MS = 25000 // 略大于 30s 探测周期，避免正常请求被误判超时

/** 带超时的 fetch，超时抛出 TimeoutError */
async function fetchWithTimeout(url: string, options?: RequestInit, timeoutMs = FETCH_TIMEOUT_MS): Promise<Response> {
  const controller = new AbortController()
  const id = setTimeout(() => controller.abort(), timeoutMs)
  try {
    const res = await fetch(url, { ...options, signal: controller.signal })
    return res
  } finally {
    clearTimeout(id)
  }
}

/** 将异常转为用户可读的错误信息 */
export function normalizeFetchError(e: unknown): string {
  if (e instanceof Error) {
    if (e.name === 'AbortError') return '请求超时，请检查网络或稍后重试'
    if (e.message.includes('Failed to fetch') || e.message.includes('NetworkError')) return '网络连接失败，请检查网络'
    return e.message
  }
  return '加载失败'
}

export async function fetchOverview(): Promise<OverviewResponse> {
  try {
    const res = await fetchWithTimeout(`${API_BASE}/overview`)
    if (!res.ok) {
      if (res.status >= 500) throw new Error('服务端暂时不可用，请稍后重试')
      if (res.status === 408) throw new Error('请求超时')
      throw new Error(`HTTP ${res.status}`)
    }
    return res.json()
  } catch (e) {
    if (e instanceof Error && e.name === 'AbortError') throw new Error('请求超时，请检查网络或稍后重试')
    throw e
  }
}

export async function fetchOpportunities(): Promise<OpportunitiesResponse> {
  const res = await fetchWithTimeout(`${API_BASE}/opportunities`)
  if (!res.ok) {
    if (res.status >= 500) throw new Error('服务端暂时不可用，请稍后重试')
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
