import { Link } from 'react-router-dom'
import { AssetSummary } from '../../../api/assets'
import AssetPreview from '../../../components/AssetPreview'
import { Badge, formatDate } from './utils'
import { thStyle, tdStyle } from './styles'

interface AssetInventoryTableProps {
  assets: AssetSummary[]
  selected: Set<string>
  onToggle: (id: string) => void
  onToggleAll: () => void
}

export function AssetInventoryTable({ assets, selected, onToggle, onToggleAll }: AssetInventoryTableProps) {
  return (
    <div style={{ overflowX: 'auto', border: '1px solid #334155', borderRadius: '8px' }}>
      <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
        <thead>
          <tr style={{ background: '#1e293b', color: '#94a3b8', textAlign: 'left' }}>
            <th style={{ ...thStyle, width: 48 }}>
              <input
                type="checkbox"
                checked={assets.length > 0 && assets.every((a) => selected.has(a.id))}
                onChange={onToggleAll}
                style={{ cursor: 'pointer' }}
              />
            </th>
            <th style={thStyle}>Anteprima</th>
            <th style={thStyle}>Nome</th>
            <th style={thStyle}>Tipo</th>
            <th style={thStyle}>Sorgente</th>
            <th style={thStyle}>Categoria</th>
            <th style={thStyle}>Stato</th>
            <th style={thStyle}>Creato</th>
            <th style={thStyle}>Azioni</th>
          </tr>
        </thead>
        <tbody>
          {assets.map((asset) => (
            <tr
              key={asset.id}
              style={{
                borderBottom: '1px solid #334155',
                background: selected.has(asset.id) ? 'rgba(56,189,248,0.1)' : undefined,
              }}
            >
              <td style={tdStyle}>
                <input
                  type="checkbox"
                  checked={selected.has(asset.id)}
                  onChange={() => onToggle(asset.id)}
                  style={{ cursor: 'pointer' }}
                />
              </td>
              <td style={tdStyle}>
                <AssetPreview id={asset.id} mediaType={asset.media_type} size={48} />
              </td>
              <td style={tdStyle}>
                <div style={{ fontWeight: 500, color: '#e2e8f0' }}>{asset.name || asset.filename}</div>
                <div style={{ fontSize: '0.75rem', color: '#64748b' }}>{asset.id}</div>
              </td>
              <td style={tdStyle}>
                <Badge text={asset.media_type} />
              </td>
              <td style={tdStyle}>
                <Badge text={asset.source} />
              </td>
              <td style={tdStyle}>{asset.category}</td>
              <td style={tdStyle}>
                <Badge text={asset.lifecycle_state} />
              </td>
              <td style={tdStyle}>{formatDate(asset.created_at)}</td>
              <td style={tdStyle}>
                <Link to={`/content/${asset.id}`} style={{ color: '#38bdf8', textDecoration: 'none', fontWeight: 500 }}>
                  Apri →
                </Link>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
