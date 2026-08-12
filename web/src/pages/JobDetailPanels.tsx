import type { CSSProperties, ReactNode } from 'react'
import type { JobFull } from '../api/jobs'

export function JobDetailBadge({ label, value }: { label: string; value?: string }) {
  if (!value) return null
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: '0.35rem', background: '#0f172a', border: '1px solid #334155', borderRadius: '6px', padding: '0.35rem 0.65rem', fontSize: '0.8rem' }}>
      <span style={{ color: '#64748b' }}>{label}:</span>
      <span style={{ color: '#e2e8f0', fontWeight: 500 }}>{value}</span>
    </div>
  )
}

export function SummaryTab({ job }: { job: JobFull }) {
  return (
    <div style={{ background: '#1e293b', border: '1px solid #334155', borderRadius: '8px', padding: '1.5rem' }}>
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
  return <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}><span style={{ color: '#94a3b8', fontSize: '0.8rem' }}>{label}</span><span style={{ color: '#e2e8f0', fontWeight: 500, wordBreak: 'break-word' }}>{value}</span></div>
}

export function TimelineTab({ events }: { events: { id?: string; type: string; message?: string; created_at?: string; data?: Record<string, unknown> }[] }) {
  if (!events.length) return <EmptyState>Nessun evento disponibile.</EmptyState>
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
      {events.map((event, idx) => (
        <div key={event.id || idx} style={{ background: '#1e293b', border: '1px solid #334155', borderRadius: '8px', padding: '1rem' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '0.5rem' }}>
            <span style={{ color: '#38bdf8', fontWeight: 600 }}>{event.type}</span>
            <span style={{ color: '#94a3b8', fontSize: '0.8rem' }}>{event.created_at ? new Date(event.created_at).toLocaleString('it-IT') : '-'}</span>
          </div>
          {event.message && <div style={{ color: '#e2e8f0', marginBottom: '0.5rem' }}>{event.message}</div>}
          {event.data && Object.keys(event.data).length > 0 && <pre style={preStyle}>{JSON.stringify(event.data, null, 2)}</pre>}
        </div>
      ))}
    </div>
  )
}

export function JsonTab({ title, data }: { title: string; data: unknown }) {
  return <div><h3 style={{ margin: '0 0 1rem', color: '#38bdf8' }}>{title}</h3>{data === undefined || data === null ? <EmptyState>Dati non disponibili.</EmptyState> : <pre style={preStyle}>{JSON.stringify(data, null, 2)}</pre>}</div>
}

export function ErrorsTab({ job }: { job: JobFull }) {
  return <div><h3 style={{ margin: '0 0 1rem', color: '#38bdf8' }}>Errori</h3>{job.error ? <div style={{ background: 'rgba(248,113,113,0.1)', border: '1px solid #f87171', color: '#f87171', padding: '1rem', borderRadius: '8px', fontFamily: 'monospace', whiteSpace: 'pre-wrap' }}>{job.error}</div> : <EmptyState>Nessun errore riportato.</EmptyState>}</div>
}

export function ArtifactsTab({ job }: { job: JobFull }) {
  const artifacts = job.result && typeof job.result === 'object' ? (job.result as Record<string, unknown>).artifacts : undefined
  if (!artifacts || !Array.isArray(artifacts) || artifacts.length === 0) return <EmptyState>Nessun artifact prodotto.</EmptyState>
  return <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>{artifacts.map((artifact: any, idx: number) => <div key={idx} style={{ background: '#1e293b', border: '1px solid #334155', borderRadius: '8px', padding: '1rem' }}><pre style={preStyle}>{JSON.stringify(artifact, null, 2)}</pre></div>)}</div>
}

export function RawJsonTab({ data }: { data: JobFull }) {
  return <div><h3 style={{ margin: '0 0 1rem', color: '#38bdf8' }}>Raw JSON</h3><pre style={preStyle}>{JSON.stringify(data, null, 2)}</pre></div>
}

export function EmptyState({ children }: { children: ReactNode }) {
  return <div style={{ color: '#94a3b8', textAlign: 'center', padding: '2rem' }}>{children}</div>
}

export const buttonStyle: CSSProperties = {
  padding: '0.55rem 1rem', background: '#38bdf8', color: '#0f172a', border: 'none', borderRadius: '6px', fontWeight: 600, cursor: 'pointer', textDecoration: 'none', display: 'inline-flex', alignItems: 'center',
}

export const tabButtonStyle: CSSProperties = {
  padding: '0.75rem 1rem', background: 'transparent', border: 'none', borderRadius: '6px 6px 0 0', cursor: 'pointer', fontSize: '0.9rem', fontWeight: 500,
}

const preStyle: CSSProperties = {
  background: '#0f172a', color: '#e2e8f0', padding: '1rem', borderRadius: '8px', fontSize: '0.75rem', overflowX: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-all', maxHeight: '60vh', overflowY: 'auto', border: '1px solid #334155',
}
