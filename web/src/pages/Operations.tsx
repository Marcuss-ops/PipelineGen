import { usePollingQuery } from '../hooks/usePollingQuery'
import RefreshButton from '../components/RefreshButton'
import {
  getHealth,
  getReady,
  getModels,
  getQdrantReady,
  getMediaIndexHealth,
  getOutboxStatus,
  getOperationsErrors,
} from '../api/client'

interface SectionState {
  loading: boolean
  error: string | null
  data: any
}

const initialSection: SectionState = { loading: true, error: null, data: null }

interface OperationsData {
  health: SectionState
  ready: SectionState
  models: SectionState
  qdrant: SectionState
  indexing: SectionState
  outbox: SectionState
  errors: SectionState
}

async function fetchOperations(): Promise<OperationsData> {
  const wrap = async (promise: Promise<unknown>): Promise<SectionState> => {
    try {
      const data = await promise
      return { loading: false, error: null, data }
    } catch (err) {
      return { loading: false, error: err instanceof Error ? err.message : 'Errore', data: null }
    }
  }

  const [health, ready, models, qdrant, indexing, outbox, errors] = await Promise.all([
    wrap(getHealth(true)),
    wrap(getReady()),
    wrap(getModels()),
    wrap(getQdrantReady()),
    wrap(getMediaIndexHealth()),
    wrap(getOutboxStatus()),
    wrap(getOperationsErrors()),
  ])

  return { health, ready, models, qdrant, indexing, outbox, errors }
}

export default function Operations() {
  const {
    data,
    loading,
    error,
    refresh,
  } = usePollingQuery<OperationsData>({
    queryFn: fetchOperations,
    interval: 10000,
  })

  const health = data?.health ?? initialSection
  const ready = data?.ready ?? initialSection
  const models = data?.models ?? initialSection
  const qdrant = data?.qdrant ?? initialSection
  const indexing = data?.indexing ?? initialSection
  const outbox = data?.outbox ?? initialSection
  const errors = data?.errors ?? initialSection

  const isLoading = loading && !data
  const sectionError = error || null

  return (
    <div style={{ padding: '2rem' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1.5rem' }}>
        <div>
          <h2 style={{ margin: 0, fontSize: '1.75rem', color: '#e2e8f0' }}>Operations</h2>
          <p style={{ margin: '0.5rem 0 0', color: '#94a3b8' }}>
            Health, modelli, Qdrant, Drive, indicizzazione, outbox ed errori operativi.
          </p>
        </div>
        <RefreshButton onClick={refresh} />
      </div>

      {isLoading ? (
        <div style={{ color: '#94a3b8', textAlign: 'center', padding: '2rem' }}>Caricamento...</div>
      ) : sectionError ? (
        <div
          style={{
            background: 'rgba(248,113,113,0.1)',
            border: '1px solid #f87171',
            color: '#f87171',
            padding: '1rem',
            borderRadius: '8px',
          }}
        >
          {sectionError}
        </div>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(360px, 1fr))', gap: '1.5rem' }}>
        <HealthCard title="Health" state={health} ready={ready} />
        <ModelsCard state={models} />
        <QdrantCard state={qdrant} />
        <DriveCard state={health} />
        <IndexingCard state={indexing} />
        <OutboxCard state={outbox} />
        <ErrorsCard state={errors} />
      </div>
      )}
    </div>
  )
}

function Section({ title, children, loading, error }: { title: string; children: React.ReactNode; loading: boolean; error: string | null }) {
  return (
    <div style={{ background: '#1e293b', border: '1px solid #334155', borderRadius: '8px', padding: '1.5rem' }}>
      <h3 style={{ margin: '0 0 1rem', fontSize: '1.1rem', color: '#e2e8f0' }}>{title}</h3>
      {loading ? <div style={{ color: '#94a3b8' }}>Caricamento...</div> : error ? <ErrorBox message={error} /> : children}
    </div>
  )
}

function ErrorBox({ message }: { message: string }) {
  return (
    <div
      style={{
        background: 'rgba(248,113,113,0.1)',
        border: '1px solid #f87171',
        color: '#f87171',
        padding: '0.75rem',
        borderRadius: '6px',
        fontSize: '0.9rem',
      }}
    >
      {message}
    </div>
  )
}

function HealthCard({ title, state, ready }: { title: string; state: SectionState; ready: SectionState }) {
  const checks = state.data?.checks as Record<string, any> | undefined
  return (
    <Section title={title} loading={state.loading} error={state.error}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
        <StatusRow label="Health" ok={state.data?.ok} />
        <StatusRow label="Ready" ok={ready.data?.ok} />
        {checks &&
          Object.entries(checks).map(([key, value]) => (
            <div key={key} style={{ display: 'flex', justifyContent: 'space-between', fontSize: '0.9rem' }}>
              <span style={{ color: '#94a3b8', textTransform: 'capitalize' }}>{key}</span>
              <span style={{ color: typeof value === 'boolean' ? (value ? '#34d399' : '#f87171') : '#e2e8f0' }}>
                {typeof value === 'boolean' ? (value ? 'OK' : 'FAIL') : JSON.stringify(value)}
              </span>
            </div>
          ))}
      </div>
    </Section>
  )
}

function ModelsCard({ state }: { state: SectionState }) {
  const models = (state.data?.models as any[]) || []
  return (
    <Section title="Modelli" loading={state.loading} error={state.error}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
        {models.length === 0 && <NoData />}
        {models.map((m, idx) => (
          <div key={`${m.model}-${idx}`} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <span style={{ color: '#94a3b8', fontSize: '0.9rem' }}>{m.model}</span>
            <span style={{ color: m.ok ? '#34d399' : '#f87171', fontWeight: 600 }}>{m.ok ? 'OK' : m.error || 'FAIL'}</span>
          </div>
        ))}
      </div>
    </Section>
  )
}

function QdrantCard({ state }: { state: SectionState }) {
  const checks = state.data?.checks as Record<string, any> | undefined
  return (
    <Section title="Qdrant" loading={state.loading} error={state.error}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
        <StatusRow label="Stato" ok={state.data?.ok} />
        {checks &&
          Object.entries(checks).map(([key, value]) => (
            <div key={key} style={{ fontSize: '0.9rem' }}>
              <span style={{ color: '#94a3b8', textTransform: 'capitalize' }}>{key}:</span>{' '}
              <span style={{ color: '#e2e8f0' }}>{typeof value === 'object' ? JSON.stringify(value) : String(value)}</span>
            </div>
          ))}
      </div>
    </Section>
  )
}

function DriveCard({ state }: { state: SectionState }) {
  const checks = (state.data?.checks as Record<string, any>) || {}
  const drive = checks.drive as any
  return (
    <Section title="Drive" loading={state.loading} error={state.error}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
        {drive ? (
          <>
            <StatusRow label="Drive check" ok={drive.ok} />
            {drive.error && <ErrorBox message={String(drive.error)} />}
            {drive.latency_ms !== undefined && (
              <div style={{ color: '#94a3b8', fontSize: '0.9rem' }}>Latency: {drive.latency_ms}ms</div>
            )}
          </>
        ) : (
          <NoData />
        )}
      </div>
    </Section>
  )
}

function IndexingCard({ state }: { state: SectionState }) {
  const indexHealth = state.data?.index_health as Record<string, any> | undefined
  return (
    <Section title="Indicizzazione" loading={state.loading} error={state.error}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
        {indexHealth ? (
          <>
            <Metric label="Asset SQLite" value={indexHealth.sqlite_assets} />
            <Metric label="Indicizzati" value={indexHealth.sqlite_indexed} />
            <Metric label="Indexabili" value={indexHealth.sqlite_indexable} />
            <Metric label="Punti Qdrant" value={indexHealth.qdrant_points} />
            <Metric label="Outbox pending" value={indexHealth.pending_outbox} />
            <Metric label="Dead letter" value={indexHealth.dead_letter} />
          </>
        ) : (
          <NoData />
        )}
      </div>
    </Section>
  )
}

function OutboxCard({ state }: { state: SectionState }) {
  const counts = (state.data?.counts as Record<string, number>) || {}
  return (
    <Section title="Outbox" loading={state.loading} error={state.error}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
        {Object.entries(counts).length === 0 && <NoData />}
        {Object.entries(counts).map(([status, count]) => (
          <div key={status} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <span style={{ color: '#94a3b8', textTransform: 'capitalize' }}>{status}</span>
            <span style={{ color: '#e2e8f0', fontWeight: 600 }}>{count}</span>
          </div>
        ))}
      </div>
    </Section>
  )
}

function ErrorsCard({ state }: { state: SectionState }) {
  const failedJobs = (state.data?.failed_jobs as any[]) || []
  const outboxErrors = (state.data?.outbox_errors as any[]) || []
  return (
    <Section title="Errori operativi" loading={state.loading} error={state.error}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
        <div>
          <h4 style={{ margin: '0 0 0.5rem', color: '#94a3b8', fontSize: '0.9rem' }}>Job falliti ({failedJobs.length})</h4>
          {failedJobs.length === 0 ? (
            <NoData />
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
              {failedJobs.map((j) => (
                <div key={j.id} style={{ background: '#0f172a', padding: '0.75rem', borderRadius: '6px' }}>
                  <div style={{ color: '#e2e8f0', fontWeight: 500 }}>{j.type}</div>
                  <div style={{ color: '#f87171', fontSize: '0.85rem' }}>{j.error || 'Errore sconosciuto'}</div>
                </div>
              ))}
            </div>
          )}
        </div>
        <div>
          <h4 style={{ margin: '0 0 0.5rem', color: '#94a3b8', fontSize: '0.9rem' }}>
            Outbox con errori ({outboxErrors.length})
          </h4>
          {outboxErrors.length === 0 ? (
            <NoData />
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
              {outboxErrors.slice(0, 10).map((e) => (
                <div key={e.ID} style={{ background: '#0f172a', padding: '0.75rem', borderRadius: '6px' }}>
                  <div style={{ color: '#e2e8f0', fontWeight: 500 }}>
                    {e.EventType} <span style={{ color: '#94a3b8' }}>({e.Status})</span>
                  </div>
                  <div style={{ color: '#f87171', fontSize: '0.85rem' }}>{e.LastError}</div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </Section>
  )
}

function StatusRow({ label, ok }: { label: string; ok: boolean | undefined }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
      <span style={{ color: '#94a3b8' }}>{label}</span>
      <span style={{ color: ok ? '#34d399' : '#f87171', fontWeight: 600 }}>{ok ? 'OK' : 'FAIL'}</span>
    </div>
  )
}

function Metric({ label, value }: { label: string; value: number | undefined }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
      <span style={{ color: '#94a3b8' }}>{label}</span>
      <span style={{ color: '#e2e8f0', fontWeight: 600 }}>{value ?? '-'}</span>
    </div>
  )
}

function NoData() {
  return <div style={{ color: '#64748b', fontSize: '0.9rem' }}>Nessun dato disponibile.</div>
}
