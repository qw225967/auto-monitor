import type { DetailPathRow } from '../types'

interface Props {
  paths: DetailPathRow[]
  renderStatus?: (status: string) => React.ReactNode
}

export function DetailTable({ paths, renderStatus }: Props) {
  if (paths.length === 0) {
    return null
  }

  return (
    <div className="detail-table-wrapper">
      <table className="detail-table">
        <thead>
          <tr>
            <th>链路 ID</th>
            <th>具体可用链路 (物理流)</th>
            <th>状态</th>
          </tr>
        </thead>
        <tbody>
          {paths.map((p) => (
            <tr key={p.path_id}>
              <td>{p.path_id}</td>
              <td className="physical-flow">{p.physical_flow}</td>
              <td>{renderStatus ? renderStatus(p.status) : p.status}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
