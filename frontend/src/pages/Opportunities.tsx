import { useEffect, useState } from 'react'
import { fetchOpportunities, type OpportunitiesResponse } from '../api'

const POLL_INTERVAL_MS = 30000

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

  if (loading && !data) return <div className="loading">加载中...</div>
  if (error) return <div className="error">{error}</div>
  if (!data || !data.opportunities) return <div className="empty-state">暂无数据</div>

  return (
    <div className="opportunities-page">
      <section className="funnel-panel">
        <h2>漏斗筛选统计</h2>
        <div className="funnel-stats">
          <div className="funnel-stat">
            <span className="stat-label">总币种数</span>
            <span className="stat-value">{data.funnel_stats.total_symbols}</span>
          </div>
          <div className="funnel-stat">
            <span className="stat-label">负价差筛选后</span>
            <span className="stat-value">{data.funnel_stats.after_negative_spread}</span>
          </div>
          <div className="funnel-stat">
            <span className="stat-label">现货深度筛选后</span>
            <span className="stat-value">{data.funnel_stats.after_spot_depth}</span>
          </div>
          <div className="funnel-stat">
            <span className="stat-label">价格斜率筛选后</span>
            <span className="stat-value">{data.funnel_stats.after_price_slope}</span>
          </div>
          <div className="funnel-stat">
            <span className="stat-label">交易量筛选后</span>
            <span className="stat-value">{data.funnel_stats.after_volume}</span>
          </div>
          <div className="funnel-stat">
            <span className="stat-label">最终机会数</span>
            <span className="stat-value highlight">{data.funnel_stats.after_both_depth}</span>
          </div>
        </div>
      </section>

      <section className="opportunities-list">
        <h2>筛选后的机会</h2>
        {(!data.opportunities || data.opportunities.length === 0) ? (
          <div className="empty-state">暂无满足条件的套利机会</div>
        ) : (
          <div className="table-container">
            <table className="opportunities-table">
              <thead>
                <tr>
                  <th>币种</th>
                  <th>现货交易所</th>
                  <th>合约交易所</th>
                  <th>价差</th>
                  <th>现货深度(USDT)</th>
                  <th>合约深度(USDT)</th>
                  <th>5分钟斜率</th>
                  <th>量能放大</th>
                  <th>置信度</th>
                </tr>
              </thead>
              <tbody>
                {data.opportunities.map((opp) => (
                  <tr key={`${opp.symbol}-${opp.spot_exchange}-${opp.futures_exchange}`}>
                    <td>{opp.symbol}</td>
                    <td>{opp.spot_exchange}</td>
                    <td>{opp.futures_exchange}</td>
                    <td className={opp.spread_percent < 0 ? 'negative' : 'positive'}>
                      {opp.spread_percent.toFixed(2)}%
                    </td>
                    <td>{opp.spot_orderbook_depth.toLocaleString()}</td>
                    <td>{opp.futures_orderbook_depth.toLocaleString()}</td>
                    <td className="positive">{(opp.price_slope_5m * 100).toFixed(4)}%</td>
                    <td>{opp.volume_spike ? '是' : '否'}</td>
                    <td>{opp.confidence}%</td>
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
