import { useEffect, useState } from 'react'
import { fetchOverview, postExchangeKeys, postLiquidityThreshold } from './api'
import { OverviewTable } from './components/OverviewTable'
import type { OverviewResponse } from './types'
import './App.css'

const POLL_INTERVAL_MS = 30000 // 30s 与后端通路探测同步

function App() {
  const [data, setData] = useState<OverviewResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [configOpen, setConfigOpen] = useState(false)
  const [keysInput, setKeysInput] = useState('')
  const [liquidityInput, setLiquidityInput] = useState('')
  const [configMsg, setConfigMsg] = useState<string | null>(null)
  const [configSubmitting, setConfigSubmitting] = useState(false)
  const [liquiditySubmitting, setLiquiditySubmitting] = useState(false)

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

  const handleSubmitKeys = async () => {
    if (!keysInput.trim()) return
    setConfigSubmitting(true)
    setConfigMsg(null)
    try {
      await postExchangeKeys(keysInput.trim())
      setConfigMsg('已保存')
      setKeysInput('') // 提交后立即清空，不保留在页面
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
    <div className="app">
      <header className="header">
        <div className="header-row">
          <div>
            <h1>加密货币搬砖监控</h1>
            <p className="subtitle">价差 10s 更新 · 通路 30s 更新</p>
          </div>
          <button
            type="button"
            className="btn-config"
            onClick={() => setConfigOpen((o) => !o)}
          >
            {configOpen ? '收起配置' : '配置'}
          </button>
        </div>
      </header>
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
      <main className="main">
        {loading && !data && <div className="loading">加载中...</div>}
        {error && <div className="error">{error}</div>}
        {data && <OverviewTable rows={data.overview} />}
      </main>
    </div>
  )
}

export default App
