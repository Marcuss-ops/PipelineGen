import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { listAssets, AssetSummary, AssetFilter } from '../api/client'
import AssetPreview from '../components/AssetPreview'

const MEDIA_TYPES = ['', 'clip', 'image', 'audio', 'document', 'image_video', 'sound_effect', 'script']
const SOURCES = ['', 'artlist', 'youtube_clip', 'stock', 'image', 'generated', 'sound_effect', 'ai_generated']
const STATES = ['', 'discovered', 'downloading', 'downloaded', 'processing', 'ready', 'error', 'archived']

interface FilterState {
  search: string
  source: string
  media_type: string
  lifecycle_state: string
  category: string
}

export default function ContentLibrary() {
  const [assets, setAssets] = useState<AssetSummary[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [cursor, setCursor] = useState<string>('')
  const [hasMore, setHasMore] = useState(false)
  const [viewMode, setViewMode] = useState<'table' | 'cards'>('table')
  const [filters, setFilters] = useState<FilterState>({
    search: '',
    source: '',
    media_type: '',
    lifecycle_state: '',
    category: '',
  })

  const loadAssets = async (nextCursor?: string, currentFilters = filters) => {
    setLoading(true)
    setError(null)
    try {
      const apiFilters: AssetFilter = {
        source: currentFilters.source,
        media_type: currentFilters.media_type,
        lifecycle_state: currentFilters.lifecycle_state,
        category: currentFilters.category,
        search: currentFilters.search,
        cursor: nextCursor,
        limit: 25,
      }
      const data = await listAssets(apiFilters)
      setAssets((prev) => (nextCursor ? [...prev, ...data.assets] : data.assets))
      setCursor(data.next_cursor)
      setHasMore(data.has_more)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Errore sconosciuto')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    const timeout = setTimeout(() => {
      loadAssets(undefined)
    }, 300)
    return () => clearTimeout(timeout)
  }, [filters.search, filters.source, filters.media_type, filters.lifecycle_state, filters.category])

  const handleFilterChange = (key: keyof FilterState, value: string) => {
    setFilters((prev) => ({ ...prev, [key]: value }))
  }

  const handleLoadMore = () => {
    if (hasMore && !loading) {
      loadAssets(cursor)
    }
  }

  const activeFilterCount = useMemo(() => {
    return Object.values(filters).filter((v) => v !== '').length
  }, [filters])

  const clearFilters = () => {
    setFilters({
      search: '',
      source: '',
      media_type: '',
      lifecycle_state: '',
      category: '',
    })
  }

  return (
    <div style={{ padding: '2rem' }}>
      <div style={{ marginBottom: '1.5rem' }}>
        <h2 style={{ margin: 0, fontSize: '1.75rem', color: '#e2e8f0' }}>Content Library</h2>
        <p style={{ margin: '0.5rem 0 0', color: '#94a3b8' }}>
          Esplora tutti gli asset indicizzati nel sistema.
        </p>
      </div>

      {/* Filters */}
      <div
        style={{
          background: '#1e293b',
          border: '1px solid #334155',
          borderRadius: '8px',
          padding: '1rem',
          marginBottom: '1.5rem',
        }}
      >
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: '1rem', alignItems: 'end' }}>
          <div style={{ flex: '1 1 240px', minWidth: '200px' }}>
            <label style={{ display: 'block', fontSize: '0.8rem', color: '#94a3b8', marginBottom: '0.35rem' }}>
              Ricerca
            </label>
            <input
              type="text"
              value={filters.search}
              onChange={(e) => handleFilterChange('search', e.target.value)}
              placeholder="Cerca per nome, ID, categoria..."
              style={inputStyle}
            />
          </div>

          <FilterSelect
            label="Tipo media"
            value={filters.media_type}
            onChange={(v) => handleFilterChange('media_type', v)}
            options={MEDIA_TYPES.map((t) => ({ value: t, label: t || 'Tutti' }))}
          />

          <FilterSelect
            label="Sorgente"
            value={filters.source}
            onChange={(v) => handleFilterChange('source', v)}
            options={SOURCES.map((s) => ({ value: s, label: s || 'Tutte' }))}
          />

          <FilterSelect
            label="Stato lifecycle"
            value={filters.lifecycle_state}
            onChange={(v) => handleFilterChange('lifecycle_state', v)}
            options={STATES.map((s) => ({ value: s, label: s || 'Tutti' }))}
          />

          <div style={{ flex: '1 1 180px', minWidth: '150px' }}>
            <label style={{ display: 'block', fontSize: '0.8rem', color: '#94a3b8', marginBottom: '0.35rem' }}>
              Categoria
            </label>
            <input
              type="text"
              value={filters.category}
              onChange={(e) => handleFilterChange('category', e.target.value)}
              placeholder="es. nature, sport..."
              style={inputStyle}
            />
          </div>
        </div>

        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: '1rem' }}>
          <div style={{ fontSize: '0.85rem', color: '#94a3b8' }}>
            {activeFilterCount > 0 ? (
              <span>
                {activeFilterCount} {activeFilterCount === 1 ? 'filtro attivo' : 'filtri attivi'}
              </span>
            ) : (
              <span>Nessun filtro attivo</span>
            )}
          </div>
          <div style={{ display: 'flex', gap: '0.5rem' }}>
            {activeFilterCount > 0 && (
              <button onClick={clearFilters} style={secondaryButtonStyle}>
                Cancella filtri
              </button>
            )}
            <div style={{ display: 'flex', border: '1px solid #334155', borderRadius: '6px', overflow: 'hidden' }}>
              <button
                onClick={() => setViewMode('table')}
                style={{
                  ...viewToggleStyle,
                  background: viewMode === 'table' ? '#38bdf8' : '#1e293b',
                  color: viewMode === 'table' ? '#0f172a' : '#e2e8f0',
                }}
              >
                Tabella
              </button>
              <button
                onClick={() => setViewMode('cards')}
                style={{
                  ...viewToggleStyle,
                  background: viewMode === 'cards' ? '#38bdf8' : '#1e293b',
                  color: viewMode === 'cards' ? '#0f172a' : '#e2e8f0',
                }}
              >
                Card
              </button>
            </div>
          </div>
        </div>
      </div>

      {/* Error */}
      {error && (
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
          {error}
        </div>
      )}

      {/* Results */}
      {loading && assets.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '3rem', color: '#94a3b8' }}>Caricamento asset...</div>
      ) : assets.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '3rem', color: '#94a3b8' }}>
          Nessun asset trovato con i filtri selezionati.
        </div>
      ) : viewMode === 'table' ? (
        <AssetTable assets={assets} />
      ) : (
        <AssetCards assets={assets} />
      )}

      {/* Load more */}
      {hasMore && (
        <div style={{ textAlign: 'center', marginTop: '1.5rem' }}>
          <button onClick={handleLoadMore} disabled={loading} style={primaryButtonStyle}>
            {loading ? 'Caricamento...' : 'Carica altri'}
          </button>
        </div>
      )}
    </div>
  )
}

function FilterSelect({
  label,
  value,
  onChange,
  options,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  options: { value: string; label: string }[]
}) {
  return (
    <div style={{ flex: '1 1 160px', minWidth: '140px' }}>
      <label style={{ display: 'block', fontSize: '0.8rem', color: '#94a3b8', marginBottom: '0.35rem' }}>{label}</label>
      <select value={value} onChange={(e) => onChange(e.target.value)} style={inputStyle}>
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </select>
    </div>
  )
}

function AssetTable({ assets }: { assets: AssetSummary[] }) {
  return (
    <div style={{ overflowX: 'auto', border: '1px solid #334155', borderRadius: '8px' }}>
      <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
        <thead>
          <tr style={{ background: '#1e293b', color: '#94a3b8', textAlign: 'left' }}>
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
            <tr key={asset.id} style={{ borderBottom: '1px solid #334155' }}>
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

function AssetCards({ assets }: { assets: AssetSummary[] }) {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))', gap: '1rem' }}>
      {assets.map((asset) => (
        <Link
          key={asset.id}
          to={`/content/${asset.id}`}
          style={{
            background: '#1e293b',
            border: '1px solid #334155',
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
          </div>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: '0.8rem' }}>
            <span style={{ color: '#94a3b8' }}>{formatDate(asset.created_at)}</span>
            <Badge text={asset.lifecycle_state} />
          </div>
        </Link>
      ))}
    </div>
  )
}

function Badge({ text }: { text: string }) {
  return (
    <span
      style={{
        display: 'inline-block',
        background: 'rgba(56,189,248,0.1)',
        color: '#38bdf8',
        padding: '0.2rem 0.5rem',
        borderRadius: '9999px',
        fontSize: '0.75rem',
        fontWeight: 500,
        whiteSpace: 'nowrap',
      }}
    >
      {text}
    </span>
  )
}

function formatDate(value: string | undefined) {
  if (!value) return '-'
  try {
    return new Date(value).toLocaleDateString('it-IT')
  } catch {
    return value
  }
}

const inputStyle: React.CSSProperties = {
  width: '100%',
  padding: '0.55rem 0.75rem',
  background: '#0f172a',
  border: '1px solid #334155',
  borderRadius: '6px',
  color: '#e2e8f0',
  fontSize: '0.9rem',
  boxSizing: 'border-box',
}

const primaryButtonStyle: React.CSSProperties = {
  padding: '0.55rem 1.25rem',
  background: '#38bdf8',
  color: '#0f172a',
  border: 'none',
  borderRadius: '6px',
  fontWeight: 600,
  cursor: 'pointer',
}

const secondaryButtonStyle: React.CSSProperties = {
  padding: '0.55rem 1rem',
  background: 'transparent',
  color: '#94a3b8',
  border: '1px solid #334155',
  borderRadius: '6px',
  cursor: 'pointer',
}

const viewToggleStyle: React.CSSProperties = {
  padding: '0.4rem 0.85rem',
  border: 'none',
  cursor: 'pointer',
  fontSize: '0.85rem',
  fontWeight: 500,
}

const thStyle: React.CSSProperties = {
  padding: '0.85rem 1rem',
  fontWeight: 600,
  fontSize: '0.8rem',
  textTransform: 'uppercase',
  letterSpacing: '0.05em',
}

const tdStyle: React.CSSProperties = {
  padding: '0.75rem 1rem',
  verticalAlign: 'middle',
}
