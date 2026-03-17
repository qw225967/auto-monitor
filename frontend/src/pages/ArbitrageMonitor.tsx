import { useEffect, useState } from 'react'
import { fetchOverview, postExchangeKeys, postLiquidityThreshold } from '../api'
import { OverviewTable } from '../components/OverviewTable'
import type { OverviewResponse } from '../types'

const POLL_INTERVAL_MS = 30000 // 30s 与后端通路探测同步

function formatAge(age?: number): string {
  if (age == null || age < 0) return '未知'
  if (age < 60) return `${age}s`
  if (age < 3600) return `${Math.floor(age / 60)}m ${age % 60}s`
  const h = Math.floor(age / 3600)
  const m = Math.floor((age % 3600) / 60)
  return `${h}h ${m}m`
}

function freshnessClass(age?: number, warnSec = 120): string {
  if (age == null) return 'freshness-unknown'
  if (age > warnSec) return 'freshness-stale'
  return 'freshness-ok'
}

type FreshnessPreset = 'strict' | 'normal' | 'relaxed'
type SortMode = 'net' | 'confidence' | 'spread'
const STORAGE_SORT_MODE = 'auto-monitor:sort-mode'
const STORAGE_FRESHNESS_PRESET = 'auto-monitor:freshness-preset'

function loadSortMode(): SortMode {
  const raw = localStorage.getItem(STORAGE_SORT_MODE)
  if (raw === 'confidence' || raw === 'spread' || raw === 'net') return raw
  return 'net'
}

function loadFreshnessPreset(): FreshnessPreset {
  const raw = localStorage.getItem(STORAGE_FRESHNESS_PRESET)
  if (raw === 'strict' || raw === 'normal' || raw === 'relaxed') return raw
  return 'normal'
}

function warnByPreset(preset: FreshnessPreset) {
  if (preset === 'strict') return { overview: 45, chain: 15, liquidity: 300 }
  if (preset === 'relaxed') return { overview: 180, chain: 60, liquidity: 1800 }
  return { overview: 90, chain: 30, liquidity: 600 }
}

export function ArbitrageMonitor() {
  const [data, setData] = useState<OverviewResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [configOpen, setConfigOpen] = useState(false)
  const [keysInput, setKeysInput] = useState('')
  const [liquidityInput, setLiquidityInput] = useState('')
  const [configMsg, setConfigMsg] = useState<string | null>(null)
  const [configSubmitting, setConfigSubmitting] = useState(false)
  const [liquiditySubmitting, setLiquiditySubmitting] = useState(false)
  const [freshnessPreset, setFreshnessPreset] = useState<FreshnessPreset>(() => loadFreshnessPreset())
  const [sortMode, setSortMode] = useState<SortMode>(() => loadSortMode())

  const load = async () => {
    try {
      setError(null)
      const res = await fetchOverview()
      setData(res)
    } catch (e) {
      setError(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    const id = setInterval(load, POLL_INTERVAL_MS)
    return () => clearInterval(id)
  }, [])

  useEffect(() => {
    localStorage.setItem(STORAGE_SORT_MODE, sortMode)
  }, [sortMode])

  useEffect(() => {
    localStorage.setItem(STORAGE_FRESHNESS_PRESET, freshnessPreset)
  }, [freshnessPreset])

  const handleSubmitKeys = async () => {
    if (!keysInput.trim()) return
    setConfigSubmitting(true)
    setConfigMsg(null)
    try {
      await postExchangeKeys(keysInput.trim())
      setConfigMsg('已保存')
      setKeysInput('')
    } catch (e) {
      setConfigMsg(e instanceof Error ? e.message : '提交失败')
    } finally {
      setConfigSubmitting(false)
    }
  }

  const handleSubmitLiquidity = async () => {
    const v = parseFloat(liquidityInput.replace(/,/g, ''))
    if (Number.isNaN(v) || v < 0) return
    setLiquiditySubmitting(true)
    setConfigMsg(null)
    try {
      await postLiquidityThreshold(v)
      setConfigMsg('流动性阈值已保存')
    } catch (e) {
      setConfigMsg(e instanceof Error ? e.message : '提交失败')
    } finally {
      setLiquiditySubmitting(false)
    }
  }

  return (
    <>
      {configOpen && (
        <section className="config-section">
          <div className="config-block">
            <p className="config-hint">流动性阈值（USDT）：低于该金额的链上流动性不展示，如输入 1000000 表示 100 万</p>
            <div className="config-row">
              <input
                type="text"
                className="config-input"
                placeholder="例：1000000"
                value={liquidityInput}
                onChange={(e) => setLiquidityInput(e.target.value)}
              />
              <button
                type="button"
                className="btn-submit"
                onClick={handleSubmitLiquidity}
                disabled={liquiditySubmitting}
              >
                {liquiditySubmitting ? '提交中...' : '设置'}
              </button>
            </div>
            {data?.liquidity_threshold != null && data.liquidity_threshold > 0 && (
              <p className="config-hint config-current">当前生效：{data.liquidity_threshold >= 10000 ? `${(data.liquidity_threshold / 10000).toFixed(0)}万` : data.liquidity_threshold} USDT</p>
            )}
          </div>
          <div className="config-block">
            <p className="config-hint">粘贴交易所 API 密钥 JSON，提交后仅存服务端内存，不落盘、不保留在页面</p>
            <textarea
              className="config-textarea"
              placeholder='{"BitGet":{"APIKey":"","Secret":"","Passphrase":""},"Bybit":{"APIKey":"","Secret":""},...}'
              value={keysInput}
              onChange={(e) => setKeysInput(e.target.value)}
              rows={8}
            />
            <div className="config-actions">
              <button
                type="button"
                className="btn-submit"
                onClick={handleSubmitKeys}
                disabled={configSubmitting || !keysInput.trim()}
              >
                {configSubmitting ? '提交中...' : '提交'}
              </button>
              {configMsg && <span className={configMsg === '已保存' || configMsg === '流动性阈值已保存' ? 'config-ok' : 'config-err'}>{configMsg}</span>}
            </div>
          </div>
        </section>
      )}
      {loading && !data && <div className="loading">加载中...</div>}
      {error && <div className="error">{error}</div>}
      {data && (
        <>
          <section className="view-controls">
            <div className="view-control-item">
              <label htmlFor="sortMode">排序</label>
              <select id="sortMode" value={sortMode} onChange={(e) => setSortMode(e.target.value as SortMode)}>
                <option value="net">按净价差</option>
                <option value="confidence">按置信度</option>
                <option value="spread">按原始价差</option>
              </select>
            </div>
            <div className="view-control-item">
              <label htmlFor="freshnessPreset">新鲜度策略</label>
              <select
                id="freshnessPreset"
                value={freshnessPreset}
                onChange={(e) => setFreshnessPreset(e.target.value as FreshnessPreset)}
              >
                <option value="strict">严格</option>
                <option value="normal">标准</option>
                <option value="relaxed">宽松</option>
              </select>
            </div>
            <div className="view-control-item" style={{ marginLeft: 'auto' }}>
              <button
                type="button"
                className="btn-config"
                onClick={() => setConfigOpen((o) => !o)}
              >
                {configOpen ? '收起配置' : '配置'}
              </button>
            </div>
          </section>
          <section className="freshness-panel">
            <div className="freshness-item">
              <span className="freshness-label">概览</span>
              <span className={`freshness-value ${freshnessClass(data.overview_age_sec, warnByPreset(freshnessPreset).overview)}`}>{formatAge(data.overview_age_sec)}</span>
            </div>
            <div className="freshness-item">
              <span className="freshness-label">链价</span>
              <span className={`freshness-value ${freshnessClass(data.chain_prices_age_sec, warnByPreset(freshnessPreset).chain)}`}>{formatAge(data.chain_prices_age_sec)}</span>
            </div>
            <div className="freshness-item">
              <span className="freshness-label">流动性</span>
              <span className={`freshness-value ${freshnessClass(data.liquidity_age_sec, warnByPreset(freshnessPreset).liquidity)}`}>{formatAge(data.liquidity_age_sec)}</span>
            </div>
          </section>
          <OverviewTable rows={data.overview ?? []} sortMode={sortMode} />
        </>
      )}
    </>
  )
}
