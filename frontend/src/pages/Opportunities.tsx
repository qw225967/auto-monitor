import { useEffect, useMemo, useState } from 'react'
import { BacktestSection } from '../components/BacktestSection'
import { fetchOpportunities, normalizeFetchError } from '../api'
import type { FunnelStats, OpportunitiesResponse } from '../types'

const POLL_INTERVAL_MS = 3000

function formatNum(n: number | undefined, digits = 2): string {
  if (n === undefined || Number.isNaN(n)) return '—'
  return n.toFixed(digits)
}

function pctDrop(prev: number, curr: number): string {
  if (prev <= 0) return ''
  const p = Math.round((1 - curr / prev) * 100)
  if (p <= 0) return ''
  return ` −${p}%`
}

type FunnelStep = {
  key: string
  label: string
  sub?: string
  value: number
  accent?: 'start' | 'mid' | 'final'
}

function buildFunnelSteps(f: FunnelStats): FunnelStep[] {
  const inRange = f.after_spread_in_range ?? f.total_symbols
  return [
    {
      key: 'total',
      label: '全量价差',
      sub: 'API 本轮条目',
      value: f.total_symbols,
      accent: 'start',
    },
    {
      key: 'range',
      label: '监控池区间',
      sub: '价差 ∈ [-1%, +1%]',
      value: inRange,
    },
    {
      key: 'anomaly',
      label: '价差突变',
      sub: '相对历史分布（σ）',
      value: f.after_spread_anomaly,
    },
    {
      key: 'accel',
      label: '斜率加速',
      sub: '价格 + 挂单量斜率',
      value: f.after_price_accel,
    },
    {
      key: 'final',
      label: '挂单猛增',
      sub: '最终机会',
      value: f.after_depth_volume,
      accent: 'final',
    },
  ]
}

export function Opportunities() {
  const [data, setData] = useState<OpportunitiesResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const load = async () => {
    try {
      setError(null)
      const res = await fetchOpportunities()
      setData(res)
    } catch (e) {
      setError(normalizeFetchError(e))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    const id = setInterval(load, POLL_INTERVAL_MS)
    return () => clearInterval(id)
  }, [])

  const opportunities = data?.opportunities ?? []
  const funnelStats = data?.funnel_stats

  const funnelSteps = useMemo(() => {
    if (!funnelStats) return []
    return buildFunnelSteps(funnelStats)
  }, [funnelStats])

  const maxFunnel = useMemo(() => {
    return Math.max(1, ...(funnelSteps.map((s) => s.value) || [1]))
  }, [funnelSteps])

  if (loading && !data) return <div className="loading">加载中...</div>
  if (error && !data) {
    return (
      <div className="error">
        {error}
        <button
          type="button"
          className="btn-retry"
          onClick={() => {
            setLoading(true)
            void load()
          }}
        >
          重试
        </button>
      </div>
    )
  }

  if (!funnelStats) return <div className="empty-state">暂无数据</div>

  return (
    <div className="opportunities-page">
      <BacktestSection />

      <section className="funnel-panel funnel-panel--elevated">
        <div className="funnel-panel__head">
          <div>
            <h2 className="funnel-panel__title">漏斗筛选</h2>
            <p className="funnel-panel__desc">
              监控池维护 [-1%, +1%] 价差统计，再按突变与多档斜率加速筛选；以下为每轮经过各层的数量。
            </p>
          </div>
          <div className="funnel-meta">
            <span className="funnel-meta__pill">
              池内 <strong>{funnelStats.watch_pool_size ?? '—'}</strong> 币种
            </span>
            <span className="funnel-meta__pill funnel-meta__pill--cool">
              冷却 <strong>{funnelStats.cooling_pool_size ?? '—'}</strong>
            </span>
            {data?.updated_at ? (
              <span className="funnel-meta__time" title="接口返回时间">
                更新 {new Date(data.updated_at).toLocaleString()}
              </span>
            ) : null}
          </div>
        </div>

        <div className="funnel-flow" role="list">
          {funnelSteps.map((step, i) => {
            const prevVal = i > 0 ? funnelSteps[i - 1].value : step.value
            const dropHint = i > 0 ? pctDrop(prevVal, step.value) : ''
            const widthPct = Math.max(8, (step.value / maxFunnel) * 100)
            return (
              <div key={step.key} className="funnel-step" role="listitem">
                <div className="funnel-step__row">
                  <div className="funnel-step__text">
                    <span className={`funnel-step__label funnel-step__label--${step.accent ?? 'mid'}`}>
                      {step.label}
                    </span>
                    {step.sub ? <span className="funnel-step__sub">{step.sub}</span> : null}
                  </div>
                  <div className="funnel-step__count">
                    <span className="funnel-step__num">{step.value.toLocaleString()}</span>
                    {dropHint ? <span className="funnel-step__drop">{dropHint}</span> : null}
                  </div>
                </div>
                <div className="funnel-step__bar-track" aria-hidden>
                  <div
                    className={`funnel-step__bar funnel-step__bar--${step.accent ?? 'mid'}`}
                    style={{ width: `${widthPct}%` }}
                  />
                </div>
                {i < funnelSteps.length - 1 ? <div className="funnel-step__connector" /> : null}
              </div>
            )
          })}
        </div>
      </section>

      <section className="opportunities-list">
        <div className="opportunities-list__head">
          <h2 className="opportunities-list__title">筛选后的机会</h2>
          <span className="opportunities-list__badge">{opportunities.length} 条</span>
        </div>
        {opportunities.length === 0 ? (
          <div className="empty-state empty-state--card">
            <p className="empty-state__title">暂无满足条件的套利机会</p>
            <p className="empty-state__hint">可查看上方漏斗，确认卡在哪一层（多为斜率或挂单加速未达标）。</p>
          </div>
        ) : (
          <div className="table-container opportunities-table-wrap">
            <table className="opportunities-table">
              <thead>
                <tr>
                  <th>币种</th>
                  <th>现货</th>
                  <th>合约 / 对手</th>
                  <th>价差</th>
                  <th title="相对历史均值的 σ 倍数">突变 σ</th>
                  <th title="短窗/长窗 价格斜率加速比">价格加速</th>
                  <th title="挂单量斜率加速比">挂单加速</th>
                  <th title="现货第一档买一量（标的数量）">买一量</th>
                  <th>置信度</th>
                </tr>
              </thead>
              <tbody>
                {opportunities.map((opp) => (
                  <tr key={`${opp.symbol}-${opp.spot_exchange}-${opp.futures_exchange}`}>
                    <td className="cell-mono cell-strong">{opp.symbol}</td>
                    <td>{opp.spot_exchange}</td>
                    <td>{opp.futures_exchange}</td>
                    <td className={opp.spread_percent < 0 ? 'negative' : 'positive'}>
                      {formatNum(opp.spread_percent)}%
                    </td>
                    <td>{formatNum(opp.spread_anomaly, 2)}σ</td>
                    <td className="positive">{formatNum(opp.price_accel_ratio, 2)}×</td>
                    <td>{formatNum(opp.volume_accel_score, 2)}×</td>
                    <td className="cell-mono">{opp.spot_orderbook_depth.toLocaleString(undefined, { maximumFractionDigits: 4 })}</td>
                    <td>
                      <span className="conf-pill">{opp.confidence}%</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  )
}
