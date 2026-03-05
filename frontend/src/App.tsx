import { useEffect, useState } from 'react'
import { fetchOverview } from './api'
import { OverviewTable } from './components/OverviewTable'
import type { OverviewResponse } from './types'
import './App.css'

const POLL_INTERVAL_MS = 30000 // 30s 与后端通路探测同步

function App() {
  const [data, setData] = useState<OverviewResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

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

  return (
    <div className="app">
      <header className="header">
        <h1>加密货币搬砖监控</h1>
        <p className="subtitle">价差 10s 更新 · 通路 30s 更新</p>
      </header>
      <main className="main">
        {loading && !data && <div className="loading">加载中...</div>}
        {error && <div className="error">{error}</div>}
        {data && <OverviewTable rows={data.overview} />}
      </main>
    </div>
  )
}

export default App
