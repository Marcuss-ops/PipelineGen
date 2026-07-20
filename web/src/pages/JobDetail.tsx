import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { getJobFull, JobFull, cancelJob, retryJob } from '../api/client'

export default function JobDetail() {
  const { id } = useParams<{ id: string }>()
  const [job, setJob] = useState<JobFull | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [activeTab, setActiveTab] = useState<'summary' | 'timeline' | 'input' | 'output' | 'errors' | 'artifacts' | 'raw'>('summary')
  const [actionLoading, setActionLoading] = useState<string | null>(null)

  const fetchJob = () => {
    if (!id) return
    setLoading(true)
    setError(null)
    getJobFull(id)
      .then(setJob)
      .catch((err) => setError(err instanceof Error ? err.message : 'Errore caricamento job'))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    fetchJob()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  const handleCancel = async () => {
    if (!id) return
    setActionLoading('cancel')
    try {
      await cancelJob(id)
      fetchJob()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Errore cancellazione')
    } finally {
      setActionLoading(null)
    }
  }

  const handleRetry = async () => {
    if (!id) return
    setActionLoading('retry')
    try {
      await retryJob(id)
      fetchJob()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Errore retry')
    } finally {
      setActionLoading(null)
    }
  }

  if (loading) {
    return <div style={{ padding: '2rem', color: '#94a3b8', textAlign: 'center' }}>Caricamento job...</div>
  }

  if (error || !job) {
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
          {error || 'Job non trovato'}
        </div>
        <Link to="/jobs" style={{ color: '#38bdf8', textDecoration: 'none' }}>
          ← Torna ai Jobs
        </Link>
      </div>
    )
  }

  const isScriptJob = job.type && (job.type.includes('script') || job.type.includes('generate'))

  return (
    <div style={{ padding: '2rem' }}>
      <div style={{ marginBottom: '1.5rem' }}>
        <Link to="/jobs" style={{ color: '#38bdf8', textDecoration: 'none', fontSize: '0.9rem' }}>
          ← Torna ai Jobs
        </Link>
      </div>

      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'flex-start',
          flexWrap: 'wrap',
          gap: '1rem',
          marginBottom: '1.5rem',
        }}
      >
        <div>
          <h2 style={{ margin: '0 0 0.5rem', fontSize: '1.75rem', color: '#e2e8f0' }}>Job {job.id.slice(0, 16)}...</h2>
          <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap' }}>
            <Badge label="Tipo" value={job.type} />
            <Badge label="Stato" value={job.status} />
            <Badge label="Stage" value={job.current_stage || job.current_step} />
            <Badge label="Progresso" value={`${job.progress || 0}%`} />
            {job.retryable !== undefined && <Badge label="Retryable" value={job.retryable ? 'Sì' : 'No'} />}
          </div>
        </div>
        <div style={{ display: 'flex', gap: '0.5rem' }}>
          {isScriptJob && (
            <Link to={`/scripts/${encodeURIComponent(job.id)}`} style={{ ...buttonStyle, background: '#818cf8' }}>
              Apri Script
            </Link>
          )}
          <button onClick={handleCancel} disabled={actionLoading === 'cancel'} style={buttonStyle}>
            {actionLoading === 'cancel' ? '...' : 'Cancella'}
          </button>
          <button onClick={handleRetry} disabled={actionLoading === 'retry'} style={buttonStyle}>
            {actionLoading === 'retry' ? '...' : 'Retry'}
          </button>
        </div>
      </div>

      {job.error && (
        <div
          style={{
            background: 'rgba(248,113,113,0.1)',
            border: '1px solid #f87171',
            color: '#f87171',
            padding: '1rem',
            borderRadius: '8px',
            marginBottom: '1.5rem',
          }}
        >
          <strong>Errore:</strong> {job.error}
        </div>
      )}

      <div style={{ display: 'flex', gap: '0.5rem', borderBottom: '1px solid #334155', marginBottom: '1.5rem' }}>
        {(
          [
            { key: 'summary', label: 'Riepilogo' },
            { key: 'timeline', label: 'Timeline eventi' },
            { key: 'input', label: 'Input' },
            { key: 'output', label: 'Output' },
            { key: 'errors', label: 'Errori' },
            { key: 'artifacts', label: 'Artifact' },
            { key: 'raw', label: 'Raw JSON' },
          ] as { key: typeof activeTab; label: string }[]
        ).map((t) => (
          <button
            key={t.key}
            onClick={() => setActiveTab(t.key)}
            style={{
              ...tabButtonStyle,
              borderBottom: activeTab === t.key ? '2px solid #38bdf8' : '2px solid transparent',
              background: activeTab === t.key ? '#0f172a' : 'transparent',
              color: activeTab === t.key ? '#38bdf8' : '#94a3b8',
            }}
          >
            {t.label}
          </button>
        ))}
      </div>

      {activeTab === 'summary' && <SummaryTab job={job} />}
      {activeTab === 'timeline' && <TimelineTab events={job.timeline || job.events || []} />}
      {activeTab === 'input' && <JsonTab title="Input / Payload" data={extractPayload(job)} />}
      {activeTab === 'output' && <JsonTab title="Output / Risultato" data={job.result} />}
      {activeTab === 'errors' && <ErrorsTab job={job} />}
      {activeTab === 'artifacts' && <ArtifactsTab job={job} />}
      {activeTab === 'raw' && <RawJsonTab data={job} />}
    </div>
  )
}

function Badge({ label, value }: { label: string; value?: string }) {
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

function SummaryTab({ job }: { job: JobFull }) {
  return (
    <div
      style={{
        background: '#1e293b',
        border: '1px solid #334155',
        borderRadius: '8px',
        padding: '1.5rem',
      }}
    >
      <h3 style={{ margin: '0 0 1rem', color: '#38bdf8' }}>Riepilogo</h3>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: '1rem' }}>
        <Field label="ID" value={job.id} />
        <Field label="Tipo" value={job.type} />
        <Field label="Stato" value={job.status} />
        <Field label="Stage corrente" value={job.current_stage || job.current_step} />
        <Field label="Progresso" value={`${job.progress || 0}%`} />
        <Field label="Correlation ID" value={job.correlation_id} />
        <Field label="Creato" value={job.created_at ? new Date(job.created_at).toLocaleString('it-IT') : undefined} />
        <Field label="Aggiornato" value={job.updated_at ? new Date(job.updated_at).toLocaleString('it-IT') : undefined} />
        <Field label="Iniziato" value={job.started_at ? new Date(job.started_at).toLocaleString('it-IT') : undefined} />
        <Field label="Retryable" value={job.retryable !== undefined ? (job.retryable ? 'Sì' : 'No') : undefined} />
      </div>
    </div>
  )
}

function Field({ label, value }: { label: string; value?: string }) {
  if (!value) return null
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
      <span style={{ color: '#94a3b8', fontSize: '0.8rem' }}>{label}</span>
      <span style={{ color: '#e2e8f0', fontWeight: 500, wordBreak: 'break-word' }}>{value}</span>
    </div>
  )
}

function TimelineTab({ events }: { events: { id?: string; type: string; message?: string; created_at?: string; data?: Record<string, unknown> }[] }) {
  if (!events.length) {
    return <EmptyState>Nessun evento disponibile.</EmptyState>
  }
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
      {events.map((ev, idx) => (
        <div
          key={ev.id || idx}
          style={{
            background: '#1e293b',
            border: '1px solid #334155',
            borderRadius: '8px',
            padding: '1rem',
          }}
        >
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '0.5rem' }}>
            <span style={{ color: '#38bdf8', fontWeight: 600 }}>{ev.type}</span>
            <span style={{ color: '#94a3b8', fontSize: '0.8rem' }}>
              {ev.created_at ? new Date(ev.created_at).toLocaleString('it-IT') : '-'}
            </span>
          </div>
          {ev.message && <div style={{ color: '#e2e8f0', marginBottom: '0.5rem' }}>{ev.message}</div>}
          {ev.data && Object.keys(ev.data).length > 0 && (
            <pre style={preStyle}>{JSON.stringify(ev.data, null, 2)}</pre>
          )}
        </div>
      ))}
    </div>
  )
}

function JsonTab({ title, data }: { title: string; data: unknown }) {
  return (
    <div>
      <h3 style={{ margin: '0 0 1rem', color: '#38bdf8' }}>{title}</h3>
      {data === undefined || data === null ? (
        <EmptyState>Dati non disponibili.</EmptyState>
      ) : (
        <pre style={preStyle}>{JSON.stringify(data, null, 2)}</pre>
      )}
    </div>
  )
}

function ErrorsTab({ job }: { job: JobFull }) {
  return (
    <div>
      <h3 style={{ margin: '0 0 1rem', color: '#38bdf8' }}>Errori</h3>
      {job.error ? (
        <div
          style={{
            background: 'rgba(248,113,113,0.1)',
            border: '1px solid #f87171',
            color: '#f87171',
            padding: '1rem',
            borderRadius: '8px',
            fontFamily: 'monospace',
            whiteSpace: 'pre-wrap',
          }}
        >
          {job.error}
        </div>
      ) : (
        <EmptyState>Nessun errore riportato.</EmptyState>
      )}
    </div>
  )
}

function ArtifactsTab({ job }: { job: JobFull }) {
  const artifacts = job.result && typeof job.result === 'object' ? (job.result as Record<string, unknown>).artifacts : undefined
  if (!artifacts || !Array.isArray(artifacts) || artifacts.length === 0) {
    return <EmptyState>Nessun artifact prodotto.</EmptyState>
  }
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
      {artifacts.map((a: any, idx: number) => (
        <div key={idx} style={{ background: '#1e293b', border: '1px solid #334155', borderRadius: '8px', padding: '1rem' }}>
          <pre style={preStyle}>{JSON.stringify(a, null, 2)}</pre>
        </div>
      ))}
    </div>
  )
}

function RawJsonTab({ data }: { data: JobFull }) {
  return (
    <div>
      <h3 style={{ margin: '0 0 1rem', color: '#38bdf8' }}>Raw JSON</h3>
      <pre style={preStyle}>{JSON.stringify(data, null, 2)}</pre>
    </div>
  )
}

function EmptyState({ children }: { children: React.ReactNode }) {
  return <div style={{ color: '#94a3b8', textAlign: 'center', padding: '2rem' }}>{children}</div>
}

function extractPayload(job: JobFull): unknown {
  if (!job.job) return undefined
  const j = job.job as Record<string, unknown>
  return j.payload ?? j.Payload ?? undefined
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

const tabButtonStyle: React.CSSProperties = {
  padding: '0.75rem 1rem',
  background: 'transparent',
  border: 'none',
  borderRadius: '6px 6px 0 0',
  cursor: 'pointer',
  fontSize: '0.9rem',
  fontWeight: 500,
}

const preStyle: React.CSSProperties = {
  background: '#0f172a',
  color: '#e2e8f0',
  padding: '1rem',
  borderRadius: '8px',
  fontSize: '0.75rem',
  overflowX: 'auto',
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-all',
  maxHeight: '60vh',
  overflowY: 'auto',
  border: '1px solid #334155',
}
