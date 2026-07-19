import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { getAsset, AssetDetails, getAssetPreviewUrl } from '../api/client'
import MetadataViewer from '../components/MetadataViewer'
import AssetPreview from '../components/AssetPreview'

export default function AssetInspector() {
  const { id } = useParams<{ id: string }>()
  const [asset, setAsset] = useState<AssetDetails | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!id) return
    setLoading(true)
    setError(null)
    getAsset(id)
      .then(setAsset)
      .catch((err) => setError(err instanceof Error ? err.message : 'Errore sconosciuto'))
      .finally(() => setLoading(false))
  }, [id])

  if (loading) {
    return (
      <div style={{ padding: '2rem', color: '#94a3b8', textAlign: 'center' }}>
        Caricamento asset...
      </div>
    )
  }

  if (error || !asset) {
    return (
      <div style={{ padding: '2rem' }}>
        <div
          style={{
            background: 'rgba(248,113,113,0.1)',
            border: '1px solid #f87171',
            color: '#f87171',
            padding: '1rem',
            borderRadius: '8px',
            marginBottom: '1rem',
          }}
        >
          {error || 'Asset non trovato'}
        </div>
        <Link to="/content" style={{ color: '#38bdf8', textDecoration: 'none' }}>
          ← Torna alla Content Library
        </Link>
      </div>
    )
  }

  return (
    <div style={{ padding: '2rem' }}>
      <div style={{ marginBottom: '1.5rem' }}>
        <Link to="/content" style={{ color: '#38bdf8', textDecoration: 'none', fontSize: '0.9rem' }}>
          ← Torna alla Content Library
        </Link>
      </div>

      <div
        style={{
          display: 'flex',
          flexWrap: 'wrap',
          gap: '1.5rem',
          marginBottom: '2rem',
          background: '#1e293b',
          border: '1px solid #334155',
          borderRadius: '8px',
          padding: '1.5rem',
        }}
      >
        <div>
          <AssetPreview
            id={asset.id}
            mediaType={asset.media_type}
            thumbnailUrl={asset.thumbnail_url}
            name={asset.name}
            size={160}
          />
        </div>
        <div style={{ flex: 1, minWidth: '280px' }}>
          <h2 style={{ margin: '0 0 0.5rem', fontSize: '1.75rem', color: '#e2e8f0' }}>
            {asset.name || asset.filename}
          </h2>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.5rem', marginBottom: '1rem' }}>
            <SummaryBadge label="ID" value={asset.id} />
            <SummaryBadge label="Tipo" value={asset.media_type} />
            <SummaryBadge label="Sorgente" value={asset.source} />
            <SummaryBadge label="Stato" value={asset.lifecycle_state} />
            <SummaryBadge label="Categoria" value={asset.category} />
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: '1rem', fontSize: '0.85rem', color: '#94a3b8' }}>
            {asset.duration && <span>Durata: {asset.duration}</span>}
            {asset.duration_secs !== undefined && <span>({asset.duration_secs} s)</span>}
            {asset.created_at && <span>Creato: {new Date(asset.created_at).toLocaleString('it-IT')}</span>}
          </div>
          <div style={{ marginTop: '1rem', display: 'flex', gap: '0.5rem' }}>
            <PreviewButton id={asset.id} mediaType={asset.media_type} />
            {asset.source_url && (
              <a
                href={asset.source_url}
                target="_blank"
                rel="noopener noreferrer"
                style={{ ...buttonStyle, background: '#1e293b', color: '#38bdf8' }}
              >
                Source URL
              </a>
            )}
          </div>
        </div>
      </div>

      <MetadataViewer asset={asset as Record<string, unknown>} title="Metadata dell'asset" />
    </div>
  )
}

function SummaryBadge({ label, value }: { label: string; value?: string }) {
  if (!value) return null
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: '0.35rem',
        background: '#0f172a',
        border: '1px solid #334155',
        borderRadius: '6px',
        padding: '0.35rem 0.65rem',
        fontSize: '0.8rem',
      }}
    >
      <span style={{ color: '#64748b' }}>{label}:</span>
      <span style={{ color: '#e2e8f0', fontWeight: 500 }}>{value}</span>
    </div>
  )
}

function PreviewButton({ id, mediaType }: { id: string; mediaType?: string }) {
  const [open, setOpen] = useState(false)
  const url = getAssetPreviewUrl(id)

  return (
    <>
      <button onClick={() => setOpen(true)} style={buttonStyle}>
        Anteprima file
      </button>
      {open && (
        <div
          style={{
            position: 'fixed',
            inset: 0,
            background: 'rgba(0,0,0,0.8)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            zIndex: 100,
            padding: '2rem',
          }}
          onClick={() => setOpen(false)}
        >
          <div
            style={{ maxWidth: '90vw', maxHeight: '90vh', position: 'relative' }}
            onClick={(e) => e.stopPropagation()}
          >
            <PreviewMedia url={url} mediaType={mediaType} />
            <button
              onClick={() => setOpen(false)}
              style={{
                position: 'absolute',
                top: '-1.5rem',
                right: '-1.5rem',
                background: '#1e293b',
                color: '#e2e8f0',
                border: '1px solid #334155',
                borderRadius: '50%',
                width: '2rem',
                height: '2rem',
                cursor: 'pointer',
              }}
            >
              ✕
            </button>
          </div>
        </div>
      )}
    </>
  )
}

function PreviewMedia({ url, mediaType }: { url: string; mediaType?: string }) {
  const lower = (mediaType || '').toLowerCase()
  if (lower.startsWith('video') || lower === 'clip') {
    return (
      <video
        src={url}
        controls
        autoPlay
        style={{ maxWidth: '100%', maxHeight: '85vh', borderRadius: '8px' }}
      />
    )
  }
  if (lower.startsWith('audio')) {
    return (
      <audio src={url} controls autoPlay style={{ maxWidth: '100%', borderRadius: '8px' }} />
    )
  }
  return (
    <img
      src={url}
      alt="preview"
      style={{ maxWidth: '100%', maxHeight: '85vh', borderRadius: '8px' }}
      onError={(e) => {
        ;(e.target as HTMLImageElement).style.display = 'none'
      }}
    />
  )
}

const buttonStyle: React.CSSProperties = {
  padding: '0.55rem 1rem',
  background: '#38bdf8',
  color: '#0f172a',
  border: 'none',
  borderRadius: '6px',
  fontWeight: 600,
  cursor: 'pointer',
  textDecoration: 'none',
  display: 'inline-flex',
  alignItems: 'center',
}
