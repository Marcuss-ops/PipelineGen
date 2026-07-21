import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { listAssets, AssetSummary, AssetFilter, bulkAssets, BulkAction, BulkChange, BulkOperationResponse } from '../api/client'
import AssetPreview from '../components/AssetPreview'

const MEDIA_TYPES = ['', 'clip', 'image', 'audio', 'document', 'image_video', 'sound_effect', 'script']
const SOURCES = ['', 'artlist', 'youtube_clip', 'stock', 'image', 'generated', 'sound_effect', 'ai_generated']
const STATES = ['', 'discovered', 'downloading', 'downloaded', 'processing', 'ready', 'error', 'archived']
const REVIEW_STATUSES = ['pending', 'approved', 'rejected', 'needs_review']
const BULK_ACTIONS: { key: BulkAction; label: string; needsPayload: boolean }[] = [
  { key: 'add_tags', label: 'Aggiungi tag', needsPayload: true },
  { key: 'remove_tags', label: 'Rimuovi tag', needsPayload: true },
  { key: 'set_category', label: 'Cambia categoria', needsPayload: true },
  { key: 'set_review_status', label: 'Imposta review status', needsPayload: true },
  { key: 'reindex', label: 'Reindicizza', needsPayload: false },
  { key: 'verify', label: 'Verifica', needsPayload: false },
  { key: 'archive', label: 'Archivia', needsPayload: false },
]

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

  // Multi-select + bulk actions state
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [bulkAction, setBulkAction] = useState<BulkAction | ''>('')
  const [bulkPayloadValue, setBulkPayloadValue] = useState('')
  const [bulkReviewStatus, setBulkReviewStatus] = useState('pending')
  const [bulkLoading, setBulkLoading] = useState(false)
  const [bulkPreview, setBulkPreview] = useState<BulkOperationResponse | null>(null)
  const [bulkError, setBulkError] = useState<string | null>(null)
  const [bulkSuccess, setBulkSuccess] = useState<string | null>(null)
  const [showBulkModal, setShowBulkModal] = useState(false)

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

  // Selection helpers
  const toggleSelect = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }

  const toggleSelectAll = () => {
    if (selected.size === assets.length && assets.length > 0) {
      setSelected(new Set())
    } else {
      setSelected(new Set(assets.map((a) => a.id)))
    }
  }

  const clearSelection = () => {
    setSelected(new Set())
  }

  // Bulk action helpers
  const resetBulk = () => {
    setBulkAction('')
    setBulkPayloadValue('')
    setBulkReviewStatus('pending')
    setBulkPreview(null)
    setBulkError(null)
    setBulkSuccess(null)
  }

  const openBulkModal = () => {
    resetBulk()
    setShowBulkModal(true)
  }

  const closeBulkModal = () => {
    setShowBulkModal(false)
    resetBulk()
  }

  const buildBulkPayload = (): Record<string, unknown> => {
    switch (bulkAction) {
      case 'add_tags':
      case 'remove_tags':
        return { tags: bulkPayloadValue.split(',').map((t) => t.trim()).filter(Boolean) }
      case 'set_category':
        return { category: bulkPayloadValue.trim() }
      case 'set_review_status':
        return { review_status: bulkReviewStatus }
      default:
        return {}
    }
  }

  const runBulk = async (dryRun: boolean) => {
    setBulkLoading(true)
    setBulkError(null)
    setBulkSuccess(null)
    try {
      const payload = buildBulkPayload()
      const res = await bulkAssets({
        asset_ids: Array.from(selected),
        action: bulkAction as BulkAction,
        dry_run: dryRun,
        payload,
      })
      setBulkPreview(res)
      if (!dryRun) {
        setBulkSuccess(`Operazione completata: ${res.affected} successo, ${res.failed} fallimenti.`)
      }
    } catch (err) {
      setBulkError(err instanceof Error ? err.message : 'Errore sconosciuto')
    } finally {
      setBulkLoading(false)
    }
  }

  const canPreview = () => {
    if (selected.size === 0) return false
    if (!bulkAction) return false
    if (bulkAction === 'add_tags' || bulkAction === 'remove_tags' || bulkAction === 'set_category') {
      return bulkPayloadValue.trim().length > 0
    }
    if (bulkAction === 'set_review_status') {
      return bulkReviewStatus.length > 0
    }
    return true
  }

  const needsPayloadInput = bulkAction === 'add_tags' || bulkAction === 'remove_tags' || bulkAction === 'set_category'

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

      {/* Bulk action toolbar */}
      {selected.size > 0 && (
        <div
          style={{
            background: 'rgba(56,189,248,0.1)',
            border: '1px solid #38bdf8',
            borderRadius: '8px',
            padding: '1rem',
            marginBottom: '1.5rem',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: '1rem',
            flexWrap: 'wrap',
          }}
        >
          <div style={{ color: '#e2e8f0', fontWeight: 500 }}>
            {selected.size} {selected.size === 1 ? 'asset selezionato' : 'asset selezionati'}
          </div>
          <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap' }}>
            <button onClick={clearSelection} style={secondaryButtonStyle}>
              Deseleziona
            </button>
            <button onClick={openBulkModal} style={primaryButtonStyle}>
              Azioni bulk →
            </button>
          </div>
        </div>
      )}

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
        <AssetTable assets={assets} selected={selected} onToggle={toggleSelect} onToggleAll={toggleSelectAll} />
      ) : (
        <AssetCards assets={assets} selected={selected} onToggle={toggleSelect} />
      )}

      {/* Load more */}
      {hasMore && (
        <div style={{ textAlign: 'center', marginTop: '1.5rem' }}>
          <button onClick={handleLoadMore} disabled={loading} style={primaryButtonStyle}>
            {loading ? 'Caricamento...' : 'Carica altri'}
          </button>
        </div>
      )}

      {/* Bulk action modal */}
      {showBulkModal && (
        <div
          style={{
            position: 'fixed',
            inset: 0,
            background: 'rgba(0,0,0,0.7)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            zIndex: 100,
            padding: '1rem',
          }}
          onClick={closeBulkModal}
        >
          <div
            style={{
              background: '#0f172a',
              border: '1px solid #334155',
              borderRadius: '12px',
              width: '100%',
              maxWidth: 720,
              maxHeight: '90vh',
              overflow: 'auto',
              padding: '1.5rem',
            }}
            onClick={(e) => e.stopPropagation()}
          >
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1rem' }}>
              <h3 style={{ margin: 0, color: '#e2e8f0' }}>Azioni bulk</h3>
              <button onClick={closeBulkModal} style={{ ...secondaryButtonStyle, padding: '0.35rem 0.75rem' }}>
                Chiudi
              </button>
            </div>

            <p style={{ color: '#94a3b8', marginTop: 0 }}>
              {selected.size} {selected.size === 1 ? 'asset selezionato' : 'asset selezionati'}.
              Scegli un'azione e verifica il preview prima di eseguire.
            </p>

            <div style={{ marginBottom: '1rem' }}>
              <label style={{ display: 'block', fontSize: '0.8rem', color: '#94a3b8', marginBottom: '0.35rem' }}>
                Azione
              </label>
              <select
                value={bulkAction}
                onChange={(e) => setBulkAction(e.target.value as BulkAction)}
                style={inputStyle}
              >
                <option value="">Seleziona azione</option>
                {BULK_ACTIONS.map((a) => (
                  <option key={a.key} value={a.key}>
                    {a.label}
                  </option>
                ))}
              </select>
            </div>

            {needsPayloadInput && (
              <div style={{ marginBottom: '1rem' }}>
                <label style={{ display: 'block', fontSize: '0.8rem', color: '#94a3b8', marginBottom: '0.35rem' }}>
                  {bulkAction === 'set_category' ? 'Nuova categoria' : 'Tag (separati da virgola)'}
                </label>
                <input
                  type="text"
                  value={bulkPayloadValue}
                  onChange={(e) => setBulkPayloadValue(e.target.value)}
                  placeholder={bulkAction === 'set_category' ? 'es. sport' : 'es. boxing, training'}
                  style={inputStyle}
                />
              </div>
            )}

            {bulkAction === 'set_review_status' && (
              <div style={{ marginBottom: '1rem' }}>
                <label style={{ display: 'block', fontSize: '0.8rem', color: '#94a3b8', marginBottom: '0.35rem' }}>
                  Review status
                </label>
                <select
                  value={bulkReviewStatus}
                  onChange={(e) => setBulkReviewStatus(e.target.value)}
                  style={inputStyle}
                >
                  {REVIEW_STATUSES.map((s) => (
                    <option key={s} value={s}>
                      {s}
                    </option>
                  ))}
                </select>
              </div>
            )}

            <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '1rem', flexWrap: 'wrap' }}>
              <button onClick={() => runBulk(true)} disabled={!canPreview() || bulkLoading} style={secondaryButtonStyle}>
                {bulkLoading && bulkPreview === null ? 'Preview...' : '🔍 Preview dry-run'}
              </button>
              <button
                onClick={() => runBulk(false)}
                disabled={!canPreview() || bulkLoading || !bulkPreview}
                style={primaryButtonStyle}
              >
                Esegui
              </button>
            </div>

            {bulkError && (
              <div
                style={{
                  background: 'rgba(248,113,113,0.1)',
                  border: '1px solid #f87171',
                  color: '#f87171',
                  padding: '0.75rem',
                  borderRadius: '6px',
                  marginBottom: '1rem',
                }}
              >
                {bulkError}
              </div>
            )}

            {bulkSuccess && (
              <div
                style={{
                  background: 'rgba(74,222,128,0.1)',
                  border: '1px solid #4ade80',
                  color: '#4ade80',
                  padding: '0.75rem',
                  borderRadius: '6px',
                  marginBottom: '1rem',
                }}
              >
                {bulkSuccess}
              </div>
            )}

            {bulkPreview && (
              <div
                style={{
                  background: '#1e293b',
                  border: '1px solid #334155',
                  borderRadius: '8px',
                  padding: '1rem',
                  marginBottom: '1rem',
                }}
              >
                <div style={{ color: '#e2e8f0', fontWeight: 600, marginBottom: '0.5rem' }}>
                  {bulkPreview.dry_run ? 'Preview dry-run' : 'Risultato esecuzione'}
                </div>
                <div style={{ color: '#94a3b8', fontSize: '0.85rem', marginBottom: '0.75rem' }}>
                  Azione: <strong>{bulkPreview.action}</strong> | Successo: {bulkPreview.affected} | Fallimenti: {bulkPreview.failed}
                </div>
                <div style={{ maxHeight: 300, overflow: 'auto' }}>
                  <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.85rem' }}>
                    <thead>
                      <tr style={{ textAlign: 'left', color: '#94a3b8' }}>
                        <th style={previewThStyle}>Asset</th>
                        <th style={previewThStyle}>Stato</th>
                        <th style={previewThStyle}>Prima</th>
                        <th style={previewThStyle}>Dopo</th>
                        <th style={previewThStyle}>Messaggio</th>
                      </tr>
                    </thead>
                    <tbody>
                      {bulkPreview.changes.map((change) => (
                        <PreviewRow key={change.asset_id} change={change} />
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            )}
          </div>
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

function AssetTable({
  assets,
  selected,
  onToggle,
  onToggleAll,
}: {
  assets: AssetSummary[]
  selected: Set<string>
  onToggle: (id: string) => void
  onToggleAll: () => void
}) {
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

function AssetCards({
  assets,
  selected,
  onToggle,
}: {
  assets: AssetSummary[]
  selected: Set<string>
  onToggle: (id: string) => void
}) {
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

function PreviewRow({ change }: { change: BulkChange }) {
  return (
    <tr style={{ borderBottom: '1px solid #334155' }}>
      <td style={previewTdStyle}>{change.asset_id}</td>
      <td style={previewTdStyle}>
        <span style={{ color: change.status === 'success' ? '#4ade80' : '#f87171' }}>
          {change.status}
        </span>
      </td>
      <td style={previewTdStyle}>
        <pre style={previewPreStyle}>{JSON.stringify(change.before, null, 2)}</pre>
      </td>
      <td style={previewTdStyle}>
        <pre style={previewPreStyle}>{JSON.stringify(change.after, null, 2)}</pre>
      </td>
      <td style={previewTdStyle}>{change.message || '-'}</td>
    </tr>
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

const previewThStyle: React.CSSProperties = {
  padding: '0.5rem 0.75rem',
  borderBottom: '1px solid #475569',
  fontSize: '0.75rem',
  textTransform: 'uppercase',
}

const previewTdStyle: React.CSSProperties = {
  padding: '0.5rem 0.75rem',
  borderBottom: '1px solid #334155',
  verticalAlign: 'top',
}

const previewPreStyle: React.CSSProperties = {
  margin: 0,
  background: '#0f172a',
  padding: '0.35rem',
  borderRadius: '4px',
  fontSize: '0.75rem',
  maxWidth: 180,
  overflow: 'auto',
}
