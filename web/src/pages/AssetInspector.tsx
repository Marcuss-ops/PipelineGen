import { useCallback, useEffect, useMemo, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { usePollingQuery } from '../hooks/usePollingQuery'
import RefreshButton from '../components/RefreshButton'
import {
  getAsset,
  AssetDetails,
  getAssetPreviewUrl,
  patchAsset,
  AssetPatchRequest,
  getAssetActions,
  AssetActionsResponse,
  triggerClipAction,
  verifyAssetIndex,
  reindexAsset,
  VerifyIndexResponse,
} from '../api/client'
import AssetPreview from '../components/AssetPreview'

type TabKey =
  | 'generale'
  | 'metadata'
  | 'indicizzazione'
  | 'files'
  | 'processing'
  | 'versions'
  | 'azioni'
  | 'raw'
  | 'audit'

interface FormState {
  name: string
  category: string
  group: string
  tags: string
  search_terms: string
  search_text: string
  review_status: string
  description: string
  language: string
}

function initialForm(asset: AssetDetails | null): FormState {
  return {
    name: asset?.name ?? '',
    category: asset?.category ?? '',
    group: asset?.group ?? '',
    tags: (asset?.tags ?? []).join(', '),
    search_terms: (asset?.search_terms ?? []).join(', '),
    search_text: asset?.search_text ?? '',
    review_status: asset?.review_status ?? '',
    description: String(asset?.metadata?.description ?? ''),
    language: String(asset?.metadata?.language ?? ''),
  }
}

function parseTags(value: string): string[] {
  return value
    .split(/[,;]/)
    .map((t) => t.trim())
    .filter((t) => t.length > 0)
}

const TRANSIENT_LIFECYCLE_STATES = ['INDEXING', 'PROCESSING', 'REPROCESSING', 'PENDING', 'READY']

function isAssetTransient(asset: AssetDetails | null): boolean {
  if (!asset) return false
  if (asset.lifecycle_state && TRANSIENT_LIFECYCLE_STATES.includes(asset.lifecycle_state)) {
    return true
  }
  return false
}

export default function AssetInspector() {
  const { id } = useParams<{ id: string }>()
  const [saving, setSaving] = useState(false)
  const [saveMsg, setSaveMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null)
  const [activeTab, setActiveTab] = useState<TabKey>('generale')
  const [actions, setActions] = useState<AssetActionsResponse | null>(null)
  const [form, setForm] = useState<FormState>(initialForm(null))
  const [dirty, setDirty] = useState(false)
  const [pausePolling, setPausePolling] = useState(false)
  const [verifyResult, setVerifyResult] = useState<VerifyIndexResponse | null>(null)
  const [verifyLoading, setVerifyLoading] = useState(false)
  const [reindexLoading, setReindexLoading] = useState(false)

  useEffect(() => {
    setVerifyResult(null)
  }, [id])

  const {
    data: asset,
    loading,
    error,
    refresh,
  } = usePollingQuery<AssetDetails>({
    queryFn: async () => {
      if (!id) throw new Error('ID mancante')
      return getAsset(id)
    },
    interval: 5000,
    enabled: !!id,
    pause: pausePolling,
  })

  useEffect(() => {
    setPausePolling(!isAssetTransient(asset))
  }, [asset])

  useEffect(() => {
    if (!id || !asset) return
    let cancelled = false
    getAssetActions(id)
      .then((acts) => {
        if (!cancelled) setActions(acts)
      })
      .catch(() => {
        if (!cancelled) setActions(null)
      })
    return () => {
      cancelled = true
    }
  }, [id, asset])

  useEffect(() => {
    setDirty(false)
    setForm(initialForm(asset))
  }, [id])

  useEffect(() => {
    if (!dirty) {
      setForm(initialForm(asset))
    }
  }, [asset, dirty])

  const load = refresh

  const changedFields = useMemo<AssetPatchRequest>(() => {
    if (!asset) return {}
    const changes: AssetPatchRequest = {}
    if (form.name !== (asset.name ?? '')) changes.name = form.name
    if (form.category !== (asset.category ?? '')) changes.category = form.category
    if (form.group !== (asset.group ?? '')) changes.group = form.group
    if (form.search_text !== (asset.search_text ?? '')) changes.search_text = form.search_text
    if (form.review_status !== (asset.review_status ?? '')) changes.review_status = form.review_status

    const tags = parseTags(form.tags)
    if (JSON.stringify(tags) !== JSON.stringify(asset.tags ?? [])) changes.tags = tags

    const searchTerms = parseTags(form.search_terms)
    if (JSON.stringify(searchTerms) !== JSON.stringify(asset.search_terms ?? [])) changes.search_terms = searchTerms

    const origDesc = String(asset.metadata?.description ?? '')
    if (form.description !== origDesc) changes.description = form.description

    const origLang = String(asset.metadata?.language ?? '')
    if (form.language !== origLang) changes.language = form.language

    return changes
  }, [form, asset])

  const handleSave = async () => {
    if (!id || !Object.keys(changedFields).length) return
    setSaving(true)
    setSaveMsg(null)
    try {
      await patchAsset(id, changedFields)
      setSaveMsg({ type: 'ok', text: 'Modifiche salvate e reindicizzazione richiesta.' })
      setDirty(false)
      await load()
    } catch (err) {
      setSaveMsg({ type: 'err', text: err instanceof Error ? err.message : 'Errore di salvataggio' })
    } finally {
      setSaving(false)
    }
  }

  const updateForm = (patch: Partial<FormState>) => {
    setForm((prev) => ({ ...prev, ...patch }))
    setDirty(true)
    setSaveMsg(null)
  }

  const runAction = async (url?: string) => {
    if (!url) return
    try {
      const res = await triggerClipAction(url)
      const msg = typeof res === 'object' && res !== null && 'message' in res
        ? String(res.message)
        : 'Azione completata'
      setSaveMsg({ type: 'ok', text: msg })
      setTimeout(() => load(), 1000)
    } catch (err) {
      setSaveMsg({ type: 'err', text: err instanceof Error ? err.message : 'Errore azione' })
    }
  }

  const handleVerify = useCallback(async () => {
    if (!id) return
    setVerifyLoading(true)
    setSaveMsg(null)
    try {
      const res = await verifyAssetIndex(id)
      setVerifyResult(res)
      setSaveMsg({ type: 'ok', text: `Verifica Qdrant completata: ${res.consistent ? 'coerente' : 'non coerente'}` })
    } catch (err) {
      setSaveMsg({ type: 'err', text: err instanceof Error ? err.message : 'Errore verifica Qdrant' })
    } finally {
      setVerifyLoading(false)
    }
  }, [id])

  const handleReindex = useCallback(async () => {
    if (!id) return
    setReindexLoading(true)
    setSaveMsg(null)
    try {
      const res = await reindexAsset(id)
      if (res.queued) {
        setSaveMsg({ type: 'ok', text: 'Reindicizzazione accodata; lo stato si aggiornerà a breve.' })
        load()
      }
    } catch (err) {
      setSaveMsg({ type: 'err', text: err instanceof Error ? err.message : 'Errore reindicizzazione' })
    } finally {
      setReindexLoading(false)
    }
  }, [id, load])

  if (loading && !asset) {
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
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1.5rem' }}>
        <Link to="/content" style={{ color: '#38bdf8', textDecoration: 'none', fontSize: '0.9rem' }}>
          ← Torna alla Content Library
        </Link>
        <RefreshButton onClick={refresh} />
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

      <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap', marginBottom: '1rem', borderBottom: '1px solid #334155' }}>
        {(
          [
            { key: 'generale', label: 'Generale' },
            { key: 'metadata', label: 'Metadata' },
            { key: 'indicizzazione', label: 'Indicizzazione' },
            { key: 'files', label: 'File e posizioni' },
            { key: 'processing', label: 'Processing' },
            { key: 'versions', label: 'Versioni' },
            { key: 'azioni', label: 'Azioni' },
            { key: 'raw', label: 'Raw JSON' },
            { key: 'audit', label: 'Audit' },
          ] as { key: TabKey; label: string }[]
        ).map((t) => (
          <button
            key={t.key}
            onClick={() => setActiveTab(t.key)}
            style={{
              ...tabButtonStyle,
              borderBottom: activeTab === t.key ? '2px solid #38bdf8' : undefined,
              background: activeTab === t.key ? '#0f172a' : '#1e293b',
            }}
          >
            {t.label}
          </button>
        ))}
      </div>

      <div
        style={{
          background: '#1e293b',
          border: '1px solid #334155',
          borderRadius: '8px',
          padding: '1.5rem',
          minHeight: '300px',
        }}
      >
        {activeTab === 'generale' && (
          <GeneralTab form={form} updateForm={updateForm} asset={asset} />
        )}
        {activeTab === 'metadata' && <MetadataTab asset={asset} />}
        {activeTab === 'indicizzazione' && (
          <IndexingTab
            asset={asset}
            onVerify={handleVerify}
            onReindex={handleReindex}
            verifyResult={verifyResult}
            verifyLoading={verifyLoading}
            reindexLoading={reindexLoading}
          />
        )}
        {activeTab === 'files' && <LocationsTab asset={asset} actions={actions} onAction={runAction} />}
        {activeTab === 'processing' && <ProcessingTab asset={asset} />}
        {activeTab === 'versions' && <VersionsTab asset={asset} />}
        {activeTab === 'azioni' && <ActionsTab actions={actions} onAction={runAction} onUpdate={handleSave} />}
        {activeTab === 'raw' && <RawJsonTab asset={asset} />}
        {activeTab === 'audit' && <AuditTab />}
      </div>

      {saveMsg && (
        <div
          style={{
            marginTop: '1rem',
            padding: '0.75rem 1rem',
            borderRadius: '6px',
            background: saveMsg.type === 'ok' ? 'rgba(52,211,153,0.1)' : 'rgba(248,113,113,0.1)',
            color: saveMsg.type === 'ok' ? '#34d399' : '#f87171',
            border: `1px solid ${saveMsg.type === 'ok' ? '#34d399' : '#f87171'}`,
          }}
        >
          {saveMsg.text}
        </div>
      )}

      <div style={{ marginTop: '1.5rem', display: 'flex', gap: '0.75rem' }}>
        <button onClick={handleSave} disabled={!dirty || saving} style={primaryButtonStyle}>
          {saving ? 'Salvataggio...' : 'Salva modifiche'}
        </button>
        <button onClick={load} style={secondaryButtonStyle}>
          Ricarica
        </button>
      </div>
    </div>
  )
}

const REVIEW_STATUS_OPTIONS = ['', 'none', 'pending', 'approved', 'rejected']

function GeneralTab({
  form,
  updateForm,
}: {
  form: FormState
  updateForm: (patch: Partial<FormState>) => void
  asset: AssetDetails
}) {
  return (
    <div style={{ display: 'grid', gap: '1rem' }}>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem' }}>
        <FormField label="Nome" value={form.name} onChange={(v) => updateForm({ name: v })} />
        <FormField label="Categoria" value={form.category} onChange={(v) => updateForm({ category: v })} />
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem' }}>
        <FormField label="Gruppo" value={form.group} onChange={(v) => updateForm({ group: v })} />
        <div>
          <label style={labelStyle}>Review status</label>
          <select
            value={form.review_status}
            onChange={(e) => updateForm({ review_status: e.target.value })}
            style={inputStyle}
          >
            {REVIEW_STATUS_OPTIONS.map((opt) => (
              <option key={opt} value={opt}>
                {opt || '(nessuno)'}
              </option>
            ))}
          </select>
        </div>
      </div>
      <FormField label="Lingua" value={form.language} onChange={(v) => updateForm({ language: v })} />
      <FormField label="Tags (separati da virgola)" value={form.tags} onChange={(v) => updateForm({ tags: v })} />
      <FormField
        label="Search terms (separati da virgola)"
        value={form.search_terms}
        onChange={(v) => updateForm({ search_terms: v })}
      />
      <div>
        <label style={labelStyle}>Search text</label>
        <textarea
          value={form.search_text}
          onChange={(e) => updateForm({ search_text: e.target.value })}
          style={{ ...inputStyle, minHeight: '80px', resize: 'vertical' }}
        />
      </div>
      <FormField label="Descrizione" value={form.description} onChange={(v) => updateForm({ description: v })} />
    </div>
  )
}

function MetadataTab({ asset }: { asset: AssetDetails }) {
  const entries = useMemo(() => {
    const out: [string, unknown][] = []
    if (asset.metadata && typeof asset.metadata === 'object') {
      for (const [k, v] of Object.entries(asset.metadata)) {
        out.push([k, v])
      }
    }
    return out
  }, [asset.metadata])

  return (
    <div>
      <h3 style={{ marginTop: 0, color: '#38bdf8' }}>Metadata</h3>
      {entries.length === 0 ? (
        <p style={{ color: '#94a3b8' }}>Nessun metadata disponibile.</p>
      ) : (
        <div style={{ display: 'grid', gap: '0.75rem' }}>
          {entries.map(([k, v]) => (
            <div
              key={k}
              style={{
                background: '#0f172a',
                border: '1px solid #334155',
                borderRadius: '6px',
                padding: '0.75rem 1rem',
              }}
            >
              <div style={{ color: '#94a3b8', fontSize: '0.8rem', marginBottom: '0.25rem' }}>{k}</div>
              <div style={{ color: '#e2e8f0', fontSize: '0.85rem', wordBreak: 'break-word' }}>
                {typeof v === 'string' ? v : JSON.stringify(v)}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function IndexingTab({
  asset,
  onVerify,
  onReindex,
  verifyResult,
  verifyLoading,
  reindexLoading,
}: {
  asset: AssetDetails
  onVerify: () => void
  onReindex: () => void
  verifyResult: VerifyIndexResponse | null
  verifyLoading: boolean
  reindexLoading: boolean
}) {
  return (
    <div>
      <h3 style={{ marginTop: 0, color: '#38bdf8' }}>Stato indicizzazione</h3>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem', marginBottom: '1.5rem' }}>
        <InfoCard label="SQLite" value={asset.lifecycle_state ? 'presente' : '-'} />
        <InfoCard label="Embedding" value={asset.embedding_info?.present ? `${asset.embedding_info.dimensions}d (${asset.embedding_info.version})` : 'mancante'} />
        <InfoCard label="Modello" value={asset.embedding_info?.version || '-'} />
        <InfoCard label="Dimensioni" value={asset.embedding_info?.dimensions ? String(asset.embedding_info.dimensions) : '-'} />
      </div>
      <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap', marginBottom: '1.5rem' }}>
        <button onClick={onReindex} disabled={reindexLoading} style={reindexLoading ? disabledButtonStyle : secondaryButtonStyle}>
          {reindexLoading ? 'Reindicizzazione...' : 'Reindicizza'}
        </button>
        <button onClick={onVerify} disabled={verifyLoading} style={verifyLoading ? disabledButtonStyle : secondaryButtonStyle}>
          {verifyLoading ? 'Verifica in corso...' : 'Verifica Qdrant'}
        </button>
      </div>
      {verifyResult && (
        <div
          style={{
            background: '#0f172a',
            border: '1px solid #334155',
            borderRadius: '8px',
            padding: '1rem',
            marginBottom: '1rem',
          }}
        >
          <h4 style={{ margin: '0 0 0.75rem', color: '#38bdf8' }}>Risultato verifica Qdrant</h4>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.75rem' }}>
            <InfoCard label="Coerente" value={verifyResult.consistent ? 'Sì' : 'No'} />
            <InfoCard label="Point presente" value={verifyResult.qdrant.point_present ? 'Sì' : 'No'} />
            <InfoCard label="Collection" value={verifyResult.qdrant.collection || '-'} />
            <InfoCard label="Dimensioni vettore" value={String(verifyResult.qdrant.vector_dimensions ?? '-')} />
            <InfoCard label="Hash corrente" value={verifyResult.sqlite.content_hash || '-'} />
            <InfoCard label="Hash indicizzato" value={verifyResult.sqlite.indexed_content_hash || '-'} />
            <InfoCard label="Embedding SQLite" value={verifyResult.sqlite.embedding_present ? 'Presente' : 'Mancante'} />
            <InfoCard label="Outbox pending" value={String(verifyResult.outbox.pending)} />
          </div>
        </div>
      )}
    </div>
  )
}

function LocationsTab({
  asset,
  actions,
  onAction,
}: {
  asset: AssetDetails
  actions: AssetActionsResponse | null
  onAction: (url?: string) => void
}) {
  return (
    <div>
      <h3 style={{ marginTop: 0, color: '#38bdf8' }}>File e posizioni</h3>
      {!asset.locations?.length && <p style={{ color: '#94a3b8' }}>Nessuna posizione disponibile.</p>}
      {asset.locations?.map((loc, idx) => (
        <div
          key={idx}
          style={{
            background: '#0f172a',
            border: '1px solid #334155',
            borderRadius: '6px',
            padding: '1rem',
            marginBottom: '0.75rem',
          }}
        >
          <div style={{ color: '#e2e8f0', fontWeight: 600 }}>{loc.kind}</div>
          <div style={{ color: '#94a3b8', fontSize: '0.85rem', wordBreak: 'break-all' }}>{loc.uri}</div>
          <div style={{ color: '#94a3b8', fontSize: '0.85rem' }}>
            {loc.external_id && <span>ID: {loc.external_id} </span>}
            {loc.file_hash && <span>Hash: {loc.file_hash} </span>}
            {loc.file_size_bytes !== undefined && <span>Size: {loc.file_size_bytes} bytes</span>}
          </div>
        </div>
      ))}
      <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap', marginTop: '1rem' }}>
        <ActionButton label="Verifica hash" url={actions?.verify} onClick={onAction} />
        <ActionButton label="Correggi hash" url={actions?.fix_hash} onClick={onAction} />
        <ActionButton label="Ricarica" url={actions?.reupload} onClick={onAction} />
        <ActionButton label="Riconcilia" url={actions?.reconcile} onClick={onAction} />
      </div>
    </div>
  )
}

function ProcessingTab({ asset }: { asset: AssetDetails }) {
  return (
    <div>
      <h3 style={{ marginTop: 0, color: '#38bdf8' }}>Processing</h3>
      {!asset.processing?.length && <p style={{ color: '#94a3b8' }}>Nessun record di processing.</p>}
      {asset.processing?.map((p, idx) => (
        <div
          key={idx}
          style={{
            background: '#0f172a',
            border: '1px solid #334155',
            borderRadius: '6px',
            padding: '1rem',
            marginBottom: '0.75rem',
          }}
        >
          <div style={{ color: '#e2e8f0', fontWeight: 600 }}>{p.step}</div>
          <div style={{ color: '#94a3b8', fontSize: '0.85rem' }}>
            Stato: {p.status} {p.error && `- ${p.error}`}
          </div>
          {p.started_at && <div style={{ fontSize: '0.8rem', color: '#64748b' }}>Iniziato: {p.started_at}</div>}
          {p.completed_at && <div style={{ fontSize: '0.8rem', color: '#64748b' }}>Completato: {p.completed_at}</div>}
        </div>
      ))}
    </div>
  )
}

function VersionsTab({ asset }: { asset: AssetDetails }) {
  return (
    <div>
      <h3 style={{ marginTop: 0, color: '#38bdf8' }}>Versioni</h3>
      {!asset.versions?.length && <p style={{ color: '#94a3b8' }}>Nessuna versione archiviata.</p>}
      {asset.versions?.map((v, idx) => (
        <div
          key={idx}
          style={{
            background: '#0f172a',
            border: '1px solid #334155',
            borderRadius: '6px',
            padding: '1rem',
            marginBottom: '0.75rem',
          }}
        >
          <div style={{ color: '#e2e8f0', fontWeight: 600 }}>Versione {v.version_number}</div>
          <div style={{ color: '#94a3b8', fontSize: '0.85rem' }}>
            {v.file_hash && <span>Hash: {v.file_hash} </span>}
            {v.file_size !== undefined && <span>Size: {v.file_size} bytes </span>}
            {v.mime_type && <span>MIME: {v.mime_type}</span>}
          </div>
          {v.created_at && <div style={{ fontSize: '0.8rem', color: '#64748b' }}>{v.created_at}</div>}
        </div>
      ))}
    </div>
  )
}

function ActionsTab({
  actions,
  onAction,
  onUpdate,
}: {
  actions: AssetActionsResponse | null
  onAction: (url?: string) => void
  onUpdate: () => void
}) {
  return (
    <div>
      <h3 style={{ marginTop: 0, color: '#38bdf8' }}>Azioni</h3>
      <div style={{ marginBottom: '1rem' }}>
        <button onClick={onUpdate} style={primaryButtonStyle}>
          💾 Update clip (salva modifiche)
        </button>
      </div>
      {!actions?.is_clip_source && <p style={{ color: '#94a3b8' }}>Azioni avanzate disponibili solo per asset clip.</p>}
      <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap' }}>
        <ActionButton label="Reindicizza" url={actions?.reindex} onClick={onAction} />
        <ActionButton label="Verifica" url={actions?.verify} onClick={onAction} />
        <ActionButton label="Riprocessa" url={actions?.reprocess} onClick={onAction} />
        <ActionButton label="Ricarica su Drive" url={actions?.reupload} onClick={onAction} />
        <ActionButton label="Correggi hash" url={actions?.fix_hash} onClick={onAction} />
        <ActionButton label="Riconcilia" url={actions?.reconcile} onClick={onAction} />
      </div>
    </div>
  )
}

function RawJsonTab({ asset }: { asset: AssetDetails }) {
  return (
    <div>
      <h3 style={{ marginTop: 0, color: '#38bdf8' }}>Raw JSON</h3>
      <pre
        style={{
          background: '#0f172a',
          color: '#e2e8f0',
          padding: '1rem',
          borderRadius: '8px',
          fontSize: '0.75rem',
          overflowX: 'auto',
          maxHeight: '60vh',
          overflowY: 'auto',
          border: '1px solid #334155',
        }}
      >
        {JSON.stringify(asset, null, 2)}
      </pre>
    </div>
  )
}

function AuditTab() {
  return (
    <div>
      <h3 style={{ marginTop: 0, color: '#38bdf8' }}>Audit log</h3>
      <p style={{ color: '#94a3b8' }}>
        L&apos;audit log amministrativo sarà disponibile dopo l&apos;implementazione della tabella
        admin_mutation_audit.
      </p>
    </div>
  )
}

function FormField({
  label: labelText,
  value,
  onChange,
  placeholder,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
}) {
  return (
    <div>
      <label style={labelStyle}>{labelText}</label>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        style={inputStyle}
      />
    </div>
  )
}

function InfoCard({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ background: '#0f172a', border: '1px solid #334155', borderRadius: '6px', padding: '1rem' }}>
      <div style={{ color: '#94a3b8', fontSize: '0.8rem', marginBottom: '0.25rem' }}>{label}</div>
      <div style={{ color: '#e2e8f0', fontWeight: 600 }}>{value}</div>
    </div>
  )
}

function ActionButton({ label, url, onClick }: { label: string; url?: string; onClick: (url?: string) => void }) {
  const disabled = !url
  return (
    <button onClick={() => onClick(url)} disabled={disabled} style={disabled ? disabledButtonStyle : secondaryButtonStyle}>
      {label}
    </button>
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

const labelStyle: React.CSSProperties = {
  display: 'block',
  fontSize: '0.85rem',
  color: '#94a3b8',
  marginBottom: '0.35rem',
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

const tabButtonStyle: React.CSSProperties = {
  padding: '0.55rem 1rem',
  background: '#1e293b',
  color: '#e2e8f0',
  border: '1px solid #334155',
  borderBottom: 'none',
  borderRadius: '6px 6px 0 0',
  cursor: 'pointer',
  fontSize: '0.85rem',
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
  background: '#1e293b',
  color: '#e2e8f0',
  border: '1px solid #334155',
  borderRadius: '6px',
  cursor: 'pointer',
}

const disabledButtonStyle: React.CSSProperties = {
  padding: '0.55rem 1rem',
  background: '#1e293b',
  color: '#64748b',
  border: '1px solid #334155',
  borderRadius: '6px',
  cursor: 'not-allowed',
  opacity: 0.6,
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
