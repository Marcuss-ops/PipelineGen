import { Link } from 'react-router-dom'
import { AssetSummary } from '../../../api/assets'
import AssetPreview from '../../../components/AssetPreview'
import { Badge, AssetStatusBadge, formatDate } from './utils'
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
            <th style={thStyle}>Asset</th>
            <th style={thStyle}>Origine</th>
            <th style={thStyle}>Tipo</th>
            <th style={thStyle}>Lifecycle</th>
            <th style={thStyle}>Journey</th>
            <th style={thStyle}>Indicizzazione</th>
            <th style={thStyle}>Storage</th>
            <th style={thStyle}>Outbox</th>
            <th style={thStyle}>Errore</th>
            <th style={thStyle}>Aggiornato</th>
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
                <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
                  <AssetPreview id={asset.id} mediaType={asset.media_type} size={40} />
                  <div>
                    <div style={{ fontWeight: 500, color: '#e2e8f0' }}>{asset.name || asset.filename}</div>
                    <div style={{ fontSize: '0.75rem', color: '#64748b' }}>{asset.id}</div>
                  </div>
                </div>
              </td>
              <td style={tdStyle}>
                <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
                  <Badge text={asset.source} />
                  <span style={{ fontSize: '0.75rem', color: '#94a3b8' }}>{asset.provider}</span>
                </div>
              </td>
              <td style={tdStyle}>
                <Badge text={asset.media_type} />
              </td>
              <td style={tdStyle}>
                <AssetStatusBadge status={asset.lifecycle_state} />
              </td>
              <td style={tdStyle}>
                <AssetStatusBadge status={asset.asset_state} />
              </td>
              <td style={tdStyle}>
                <AssetStatusBadge status={asset.index_health} />
              </td>
              <td style={tdStyle}>
                <StorageIndicator asset={asset} />
              </td>
              <td style={tdStyle}>
                <span style={{ color: asset.pending_outbox_events > 0 ? '#facc15' : '#64748b' }}>
                  {asset.pending_outbox_events}
                </span>
              </td>
              <td style={tdStyle}>
                <span
                  title={asset.last_error || undefined}
                  style={{
                    color: asset.last_error ? '#f87171' : '#64748b',
                    maxWidth: '120px',
                    display: 'inline-block',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {asset.last_error || '-'}
                </span>
              </td>
              <td style={tdStyle}>{formatDate(asset.updated_at)}</td>
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

function StorageIndicator({ asset }: { asset: AssetSummary }) {
  const parts: { key: string; label: string }[] = []
  if (asset.has_local_file) parts.push({ key: 'local', label: 'LOCAL' })
  if (asset.has_drive_file) parts.push({ key: 'drive', label: 'DRIVE' })
  if (asset.has_embedding) parts.push({ key: 'embedding', label: 'EMB' })

  if (parts.length === 0) {
    return <span style={{ color: '#64748b', fontSize: '0.75rem' }}>mancante</span>
  }

  return (
    <div style={{ display: 'flex', gap: '0.35rem', flexWrap: 'wrap' }}>
      {parts.map((p) => (
        <span
          key={p.key}
          style={{
            display: 'inline-block',
            background: 'rgba(56,189,248,0.1)',
            color: '#38bdf8',
            padding: '0.15rem 0.4rem',
            borderRadius: '4px',
            fontSize: '0.7rem',
            fontWeight: 500,
            whiteSpace: 'nowrap',
          }}
        >
          {p.label}
        </span>
      ))}
    </div>
  )
}
