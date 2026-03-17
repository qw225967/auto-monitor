import { Link, useLocation } from 'react-router-dom'

export function Layout({ children }: { children: React.ReactNode }) {
  const location = useLocation()

  return (
    <div className="app">
      <header className="header">
        <div className="header-row">
          <div>
            <h1>加密货币搬砖监控</h1>
            <p className="subtitle">价差 10s 更新 · 通路 30s 更新</p>
          </div>
          <nav className="nav-tabs">
            <Link to="/" className={location.pathname === '/' ? 'active' : ''}>搬砖监控</Link>
            <Link to="/opportunities" className={location.pathname === '/opportunities' ? 'active' : ''}>机会发现</Link>
          </nav>
        </div>
      </header>
      <main className="main">{children}</main>
    </div>
  )
}
