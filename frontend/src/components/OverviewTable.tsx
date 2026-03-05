import React, { useState } from 'react'
import type { OverviewRow } from '../types'
import { DetailTable } from './DetailTable'

interface Props {
  rows: OverviewRow[]
}

function StatusBadge({ status }: { status: string }) {
  const map: Record<string, { icon: string; label: string; className: string }> = {
    ok: { icon: '✅', label: '畅通', className: 'status-ok' },
    maintenance: { icon: '⚠️', label: '维护中', className: 'status-maintenance' },
    unavailable: { icon: '❌', label: '不可用', className: 'status-unavailable' },
  }
  const s = map[status] ?? { icon: '?', label: status, className: '' }
  return (
    <span className={`status-badge ${s.className}`}>
      {s.icon} {s.label}
    </span>
  )
}

export function OverviewTable({ rows }: Props) {
  const [expandedSymbol, setExpandedSymbol] = useState<string | null>(null)

  const toggleExpand = (symbol: string) => {
    setExpandedSymbol((prev) => (prev === symbol ? null : symbol))
  }

  if (rows.length === 0) {
    return (
      <div className="empty-state">
        暂无数据，请稍后刷新（价差 10s 更新，通路 30s 更新）
      </div>
    )
  }

  return (
    <div className="table-container">
      <table className="overview-table">
        <thead>
          <tr>
            <th>币种</th>
            <th>路径 (买入 → 卖出)</th>
            <th>原始价差</th>
            <th>可用通路数</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <React.Fragment key={row.symbol}>
              <tr>
                <td>{row.symbol}</td>
                <td>{row.path_display}</td>
                <td>{row.spread_percent.toFixed(2)}%</td>
                <td>{row.available_path_count}条</td>
                <td>
                  <button
                    type="button"
                    className="btn-detail"
                    onClick={() => toggleExpand(row.symbol)}
                  >
                    {expandedSymbol === row.symbol ? '收起详情' : '查看详情'}
                  </button>
                </td>
              </tr>
              {expandedSymbol === row.symbol && (
                <tr>
                  <td colSpan={5} className="detail-cell">
                    <DetailTable
                      paths={row.detail_paths}
                      renderStatus={(s) => <StatusBadge status={s} />}
                    />
                  </td>
                </tr>
              )}
            </React.Fragment>
          ))}
        </tbody>
      </table>
    </div>
  )
}
