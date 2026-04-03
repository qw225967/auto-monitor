import * as echarts from 'echarts'
import { useEffect, useRef, useState } from 'react'
import { normalizeFetchError, postBacktestRun } from '../api'
import type { BacktestResponse } from '../types'

function useBacktestCharts(data: BacktestResponse | null) {
  const spreadRef = useRef<HTMLDivElement>(null)
  const priceRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!data || !spreadRef.current || !priceRef.current) return

    const times = data.spread_series.map((p) => p.t)
    const spreadVals = data.spread_series.map((p) => p.v)
    const priceVals = data.price_series.map((p) => p.v)

    const markLineData = data.signals.map((s) => ({
      xAxis: s.t,
      lineStyle: { color: '#e67e22', type: 'dashed' },
      label: { show: true, formatter: `${s.layer} · σ机会`, fontSize: 10 },
    }))

    const baseGrid = { left: 56, right: 24, bottom: 72, top: 36 }
    const baseX = {
      type: 'category' as const,
      data: times,
      axisLabel: { rotate: 35, fontSize: 10 },
    }

    const sChart = echarts.init(spreadRef.current)
    sChart.setOption({
      title: { text: '价差 %（现货 vs U 本位）', left: 'center', textStyle: { fontSize: 14 } },
      tooltip: { trigger: 'axis' },
      grid: baseGrid,
      xAxis: baseX,
      yAxis: { type: 'value', name: '%' },
      series: [
        {
          name: '价差',
          type: 'line',
          data: spreadVals,
          showSymbol: false,
          smooth: 0.2,
          lineStyle: { width: 1.5 },
          markLine: data.signals.length
            ? {
                symbol: 'none',
                data: markLineData,
              }
            : undefined,
        },
      ],
    })

    const pChart = echarts.init(priceRef.current)
    pChart.setOption({
      title: { text: '现货收盘价（Binance）', left: 'center', textStyle: { fontSize: 14 } },
      tooltip: { trigger: 'axis' },
      grid: baseGrid,
      xAxis: baseX,
      yAxis: { type: 'value', name: 'USDT' },
      series: [
        {
          name: '价格',
          type: 'line',
          data: priceVals,
          showSymbol: false,
          smooth: 0.2,
          lineStyle: { width: 1.5, color: '#3498db' },
          markLine: data.signals.length
            ? {
                symbol: 'none',
                data: markLineData,
              }
            : undefined,
        },
      ],
    })

    const ro = new ResizeObserver(() => {
      sChart.resize()
      pChart.resize()
    })
    ro.observe(spreadRef.current)
    ro.observe(priceRef.current)

    return () => {
      ro.disconnect()
      sChart.dispose()
      pChart.dispose()
    }
  }, [data])

  return { spreadRef, priceRef }
}

export function BacktestSection() {
  const [symbol, setSymbol] = useState('BTCUSDT')
  const [fromLocal, setFromLocal] = useState('')
  const [toLocal, setToLocal] = useState('')
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const [data, setData] = useState<BacktestResponse | null>(null)

  const { spreadRef, priceRef } = useBacktestCharts(data)

  useEffect(() => {
    const to = new Date()
    const from = new Date(to.getTime() - 24 * 60 * 60 * 1000)
    const fmt = (d: Date) => {
      const pad = (n: number) => String(n).padStart(2, '0')
      return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
    }
    setToLocal(fmt(to))
    setFromLocal(fmt(from))
  }, [])

  const run = async () => {
    setErr(null)
    setLoading(true)
    setData(null)
    try {
      const from = new Date(fromLocal)
      const to = new Date(toLocal)
      if (Number.isNaN(from.getTime()) || Number.isNaN(to.getTime())) {
        throw new Error('时间格式无效')
      }
      const res = await postBacktestRun({
        symbol: symbol.trim().toUpperCase(),
        from: from.toISOString(),
        to: to.toISOString(),
      })
      setData(res)
    } catch (e) {
      setErr(normalizeFetchError(e))
    } finally {
      setLoading(false)
    }
  }

  return (
    <section className="backtest-section">
      <div className="backtest-section__head">
        <h2 className="backtest-section__title">漏斗回测</h2>
        <p className="backtest-section__desc">
          使用 Binance 现货与 U 本位 1m 历史 K 线本地算价差并逐步回放监控池与漏斗（无订单簿时降级为层1）。
          最长 7 天。
        </p>
      </div>
      <div className="backtest-form">
        <label className="backtest-form__field">
          <span>Symbol</span>
          <input
            type="text"
            value={symbol}
            onChange={(e) => setSymbol(e.target.value)}
            placeholder="BTCUSDT"
            className="backtest-form__input"
          />
        </label>
        <label className="backtest-form__field">
          <span>开始</span>
          <input
            type="datetime-local"
            value={fromLocal}
            onChange={(e) => setFromLocal(e.target.value)}
            className="backtest-form__input"
          />
        </label>
        <label className="backtest-form__field">
          <span>结束</span>
          <input
            type="datetime-local"
            value={toLocal}
            onChange={(e) => setToLocal(e.target.value)}
            className="backtest-form__input"
          />
        </label>
        <button type="button" className="backtest-form__btn" disabled={loading} onClick={() => void run()}>
          {loading ? '加载中…' : '加载回测'}
        </button>
      </div>
      {err ? <div className="backtest-error">{err}</div> : null}
      {data?.warnings?.length ? (
        <ul className="backtest-warnings">
          {data.warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      ) : null}
      {data ? (
        <p className="backtest-meta">
          粒度 <strong>{data.granularity}</strong> · 现货 {data.spot_exchange} · {data.futures_exchange} · 信号{' '}
          <strong>{data.signals.length}</strong> 个
        </p>
      ) : null}
      {data?.signals?.length ? (
        <ul className="backtest-signals-list">
          {data.signals.map((s, i) => (
            <li key={`${s.t}-${i}`}>
              <time dateTime={s.t}>{new Date(s.t).toLocaleString()}</time> — {s.layer}: {s.message}
              {s.spread_percent != null ? ` · 价差 ${s.spread_percent.toFixed(4)}%` : ''}
              {s.confidence != null ? ` · 置信 ${s.confidence}%` : ''}
            </li>
          ))}
        </ul>
      ) : null}
      <div className="backtest-charts">
        <div ref={spreadRef} className="backtest-chart" />
        <div ref={priceRef} className="backtest-chart" />
      </div>
    </section>
  )
}
