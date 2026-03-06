import React, { useState } from 'react'
import type { OverviewRow } from '../types'
import { OppTypeLabels } from '../types'
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
            <th>类型</th>
            <th>币种</th>
            <th>路径 (买入 → 卖出)</th>
            <th>原始价差</th>
            <th>可用通路数</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row, idx) => (
            <React.Fragment key={`${row.symbol}-${row.path_display}-${idx}`}>
              <tr>
                <td>{OppTypeLabels[row.type ?? 'cex_cex'] ?? row.type}</td>
                <td>{row.symbol}</td>
                <td>
                  {row.path_display}
                  {row.chain_liquidity && (
                    <span className="chain-liquidity"> · {row.chain_liquidity}</span>
                  )}
                </td>
                <td>{row.spread_percent.toFixed(2)}%</td>
                <td>{(row.available_path_count ?? 0)}条</td>
                <td>
                  {(row.detail_paths?.length ?? 0) > 0 ? (
                    <button
                      type="button"
                      className="btn-detail"
                      onClick={() => toggleExpand(`${row.symbol}-${row.path_display}`)}
                    >
                      {expandedSymbol === `${row.symbol}-${row.path_display}` ? '收起详情' : '查看详情'}
                    </button>
                  ) : (
                    <span className="no-detail">-</span>
                  )}
                </td>
              </tr>
              {expandedSymbol === `${row.symbol}-${row.path_display}` && (
                <tr>
                  <td colSpan={6} className="detail-cell">
                    <DetailTable
                      paths={row.detail_paths ?? []}
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
