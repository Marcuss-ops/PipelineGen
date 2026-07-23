import { Link } from 'react-router-dom'
import { AssetSummary } from '../../../api/assets'
import AssetPreview from '../../../components/AssetPreview'
import { Badge, formatDate } from './utils'

interface AssetInventoryCardsProps {
  assets: AssetSummary[]
  selected: Set<string>
  onToggle: (id: string) => void
}

export function AssetInventoryCards({ assets, selected, onToggle }: AssetInventoryCardsProps) {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))', gap: '1rem' }}>
      {assets.map((asset) => (
        <div
          key={asset.id}
          style={{
            background: '#1e293b',
            border: selected.has(asset.id) ? '2px solid #38bdf8' : '1px solid #334155',
            borderRadius: '8px',
            padding: '1rem',
            textDecoration: 'none',
            color: 'inherit',
            transition: 'transform 0.15s ease, box-shadow 0.15s ease',
          }}
        >
          <div style={{ display: 'flex', gap: '1rem', marginBottom: '0.75rem' }}>
            <AssetPreview id={asset.id} mediaType={asset.media_type} size={80} />
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontWeight: 600, color: '#e2e8f0', marginBottom: '0.25rem', wordBreak: 'break-word' }}>
                {asset.name || asset.filename}
              </div>
              <div style={{ fontSize: '0.75rem', color: '#64748b', marginBottom: '0.5rem' }}>{asset.id}</div>
              <div style={{ display: 'flex', gap: '0.35rem', flexWrap: 'wrap' }}>
                <Badge text={asset.media_type} />
                <Badge text={asset.source} />
              </div>
            </div>
            <input
              type="checkbox"
              checked={selected.has(asset.id)}
              onChange={() => onToggle(asset.id)}
              style={{ cursor: 'pointer', width: 18, height: 18 }}
            />
          </div>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: '0.8rem' }}>
            <span style={{ color: '#94a3b8' }}>{formatDate(asset.created_at)}</span>
            <Badge text={asset.lifecycle_state} />
          </div>
          <Link
            to={`/content/${asset.id}`}
            style={{ display: 'block', marginTop: '0.75rem', color: '#38bdf8', textDecoration: 'none', fontWeight: 500 }}
          >
            Apri →
          </Link>
        </div>
      ))}
    </div>
  )
}
