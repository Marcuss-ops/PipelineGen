import type { ReactNode } from 'react'

export interface SectionState {
  loading: boolean
  error: string | null
  data: any
}

export function Section({ title, children, loading, error }: { title: string; children: ReactNode; loading: boolean; error: string | null }) {
  return <div style={{ background: '#1e293b', border: '1px solid #334155', borderRadius: '8px', padding: '1.5rem' }}><h3 style={{ margin: '0 0 1rem', fontSize: '1.1rem', color: '#e2e8f0' }}>{title}</h3>{loading ? <div style={{ color: '#94a3b8' }}>Caricamento...</div> : error ? <ErrorBox message={error} /> : children}</div>
}

export function ErrorBox({ message }: { message: string }) {
  return <div style={{ background: 'rgba(248,113,113,0.1)', border: '1px solid #f87171', color: '#f87171', padding: '0.75rem', borderRadius: '6px', fontSize: '0.9rem' }}>{message}</div>
}

export function HealthCard({ title, state, ready }: { title: string; state: SectionState; ready: SectionState }) {
  const checks = state.data?.checks as Record<string, any> | undefined
  return <Section title={title} loading={state.loading} error={state.error}><div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}><StatusRow label="Health" ok={state.data?.ok} /><StatusRow label="Ready" ok={ready.data?.ok} />{checks && Object.entries(checks).map(([key, value]) => <div key={key} style={{ display: 'flex', justifyContent: 'space-between', fontSize: '0.9rem' }}><span style={{ color: '#94a3b8', textTransform: 'capitalize' }}>{key}</span><span style={{ color: typeof value === 'boolean' ? (value ? '#34d399' : '#f87171') : '#e2e8f0' }}>{typeof value === 'boolean' ? (value ? 'OK' : 'FAIL') : JSON.stringify(value)}</span></div>)}</div></Section>
}

export function ModelsCard({ state }: { state: SectionState }) {
  const models = (state.data?.models as any[]) || []
  return <Section title="Modelli" loading={state.loading} error={state.error}><div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>{models.length === 0 && <NoData />}{models.map((model, idx) => <div key={`${model.model}-${idx}`} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}><span style={{ color: '#94a3b8', fontSize: '0.9rem' }}>{model.model}</span><span style={{ color: model.ok ? '#34d399' : '#f87171', fontWeight: 600 }}>{model.ok ? 'OK' : model.error || 'FAIL'}</span></div>)}</div></Section>
}

export function QdrantCard({ state }: { state: SectionState }) {
  const checks = state.data?.checks as Record<string, any> | undefined
  return <Section title="Qdrant" loading={state.loading} error={state.error}><div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}><StatusRow label="Stato" ok={state.data?.ok} />{checks && Object.entries(checks).map(([key, value]) => <div key={key} style={{ fontSize: '0.9rem' }}><span style={{ color: '#94a3b8', textTransform: 'capitalize' }}>{key}:</span>{' '}<span style={{ color: '#e2e8f0' }}>{typeof value === 'object' ? JSON.stringify(value) : String(value)}</span></div>)}</div></Section>
}

export function DriveCard({ state }: { state: SectionState }) {
  const checks = (state.data?.checks as Record<string, any>) || {}
  const drive = checks.drive as any
  return <Section title="Drive" loading={state.loading} error={state.error}><div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>{drive ? <><StatusRow label="Drive check" ok={drive.ok} />{drive.error && <ErrorBox message={String(drive.error)} />}{drive.latency_ms !== undefined && <div style={{ color: '#94a3b8', fontSize: '0.9rem' }}>Latency: {drive.latency_ms}ms</div>}</> : <NoData />}</div></Section>
}

export function IndexingCard({ state }: { state: SectionState }) {
  const indexHealth = state.data?.index_health as Record<string, any> | undefined
  return <Section title="Indicizzazione" loading={state.loading} error={state.error}><div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>{indexHealth ? <><Metric label="Asset SQLite" value={indexHealth.sqlite_assets} /><Metric label="Indicizzati" value={indexHealth.sqlite_indexed} /><Metric label="Indexabili" value={indexHealth.sqlite_indexable} /><Metric label="Punti Qdrant" value={indexHealth.qdrant_points} /><Metric label="Outbox pending" value={indexHealth.pending_outbox} /><Metric label="Dead letter" value={indexHealth.dead_letter} /></> : <NoData />}</div></Section>
}

export function OutboxCard({ state }: { state: SectionState }) {
  const counts = (state.data?.counts as Record<string, number>) || {}
  return <Section title="Outbox" loading={state.loading} error={state.error}><div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>{Object.entries(counts).length === 0 && <NoData />}{Object.entries(counts).map(([status, count]) => <div key={status} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}><span style={{ color: '#94a3b8', textTransform: 'capitalize' }}>{status}</span><span style={{ color: '#e2e8f0', fontWeight: 600 }}>{count}</span></div>)}</div></Section>
}

export function ErrorsCard({ state }: { state: SectionState }) {
  const failedJobs = (state.data?.failed_jobs as any[]) || []
  const outboxErrors = (state.data?.outbox_errors as any[]) || []
  return <Section title="Errori operativi" loading={state.loading} error={state.error}><div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}><div><h4 style={{ margin: '0 0 0.5rem', color: '#94a3b8', fontSize: '0.9rem' }}>Job falliti ({failedJobs.length})</h4>{failedJobs.length === 0 ? <NoData /> : <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>{failedJobs.map((job) => <div key={job.id} style={{ background: '#0f172a', padding: '0.75rem', borderRadius: '6px' }}><div style={{ color: '#e2e8f0', fontWeight: 500 }}>{job.type}</div><div style={{ color: '#f87171', fontSize: '0.85rem' }}>{job.error || 'Errore sconosciuto'}</div></div>)}</div>}</div><div><h4 style={{ margin: '0 0 0.5rem', color: '#94a3b8', fontSize: '0.9rem' }}>Outbox con errori ({outboxErrors.length})</h4>{outboxErrors.length === 0 ? <NoData /> : <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>{outboxErrors.slice(0, 10).map((event) => <div key={event.ID} style={{ background: '#0f172a', padding: '0.75rem', borderRadius: '6px' }}><div style={{ color: '#e2e8f0', fontWeight: 500 }}>{event.EventType} <span style={{ color: '#94a3b8' }}>({event.Status})</span></div><div style={{ color: '#f87171', fontSize: '0.85rem' }}>{event.LastError}</div></div>)}</div>}</div></div></Section>
}

function StatusRow({ label, ok }: { label: string; ok: boolean | undefined }) {
  return <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}><span style={{ color: '#94a3b8' }}>{label}</span><span style={{ color: ok ? '#34d399' : '#f87171', fontWeight: 600 }}>{ok ? 'OK' : 'FAIL'}</span></div>
}

function Metric({ label, value }: { label: string; value: number | undefined }) {
  return <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}><span style={{ color: '#94a3b8' }}>{label}</span><span style={{ color: '#e2e8f0', fontWeight: 600 }}>{value ?? '-'}</span></div>
}

function NoData() {
  return <div style={{ color: '#64748b', fontSize: '0.9rem' }}>Nessun dato disponibile.</div>
}
