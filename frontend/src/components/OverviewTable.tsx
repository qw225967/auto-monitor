import React, { useState } from 'react'
import type { OverviewRow } from '../types'
import { OppTypeLabels } from '../types'
import { DetailTable } from './DetailTable'

interface Props {
  rows: OverviewRow[]
  sortMode?: 'net' | 'confidence' | 'spread'
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

function spreadForSort(row: OverviewRow): number {
  if (row.net_spread_percent != null) return row.net_spread_percent
  if (row.gross_spread_percent != null) return row.gross_spread_percent
  return row.spread_percent ?? 0
}

export function OverviewTable({ rows, sortMode = 'net' }: Props) {
  const [expandedSymbol, setExpandedSymbol] = useState<string | null>(null)

  const toggleExpand = (symbol: string) => {
    setExpandedSymbol((prev) => (prev === symbol ? null : symbol))
  }

  const list = [...(rows ?? [])].sort((a, b) => {
    if (sortMode === 'confidence') {
      const ai = a.confidence_score ?? 0
      const bi = b.confidence_score ?? 0
      if (bi !== ai) return bi - ai
      return spreadForSort(b) - spreadForSort(a)
    }
    if (sortMode === 'spread') {
      return (b.spread_percent ?? 0) - (a.spread_percent ?? 0)
    }
    return spreadForSort(b) - spreadForSort(a)
  })
  if (list.length === 0) {
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
            <th>净价差</th>
            <th>置信度</th>
            <th>可用通路数</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          {list.map((row, idx) => (
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
                <td>
                  {row.net_spread_percent != null
                    ? `${row.net_spread_percent.toFixed(2)}%`
                    : row.gross_spread_percent != null
                      ? `${row.gross_spread_percent.toFixed(2)}%`
                      : '-'}
                </td>
                <td>
                  {row.confidence_score != null
                    ? `${(row.confidence_score * 100).toFixed(0)}%`
                    : '-'}
                </td>
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
                  <td colSpan={8} className="detail-cell">
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
