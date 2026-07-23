import { usePollingQuery } from '../hooks/usePollingQuery'
import RefreshButton from '../components/RefreshButton'
import { getSummary } from '../api/assets'

interface SummaryData {
  total_assets?: number
  by_source?: Record<string, number>
  by_media_type?: Record<string, number>
  indexed?: number
  non_indexed?: number
  local_count?: number
  drive_count?: number
  jobs_running?: number
  jobs_failed?: number
  jobs_completed?: number
  outbox_pending?: number
  outbox_failed?: number
}

export default function Overview() {
  const {
    data: summary,
    loading,
    error,
    refresh,
  } = usePollingQuery<SummaryData>({
    queryFn: async () => (await getSummary()) as SummaryData,
    interval: 5000,
  })

  return (
    <div style={{ padding: '2rem' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1.5rem' }}>
        <div>
          <h2 style={{ margin: 0, fontSize: '1.75rem', color: '#e2e8f0' }}>Overview</h2>
          <p style={{ margin: '0.5rem 0 0', color: '#94a3b8' }}>
            Panoramica del sistema e degli asset indicizzati.
          </p>
        </div>
        <RefreshButton onClick={refresh} />
      </div>

      {loading ? (
        <div style={{ color: '#94a3b8' }}>Caricamento...</div>
      ) : error ? (
        <div
          style={{
            background: 'rgba(248,113,113,0.1)',
            border: '1px solid #f87171',
            color: '#f87171',
            padding: '1rem',
            borderRadius: '8px',
          }}
        >
          {error}
        </div>
      ) : (
        <>
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))',
              gap: '1rem',
              marginBottom: '2rem',
            }}
          >
            <MetricCard label="Asset totali" value={summary?.total_assets ?? 0} color="#38bdf8" />
            <MetricCard label="Indicizzati" value={summary?.indexed ?? 0} color="#34d399" />
            <MetricCard label="Non indicizzati" value={summary?.non_indexed ?? 0} color="#fbbf24" />
            <MetricCard label="Locali" value={summary?.local_count ?? 0} color="#a78bfa" />
            <MetricCard label="Drive" value={summary?.drive_count ?? 0} color="#60a5fa" />
            <MetricCard label="Job in esecuzione" value={summary?.jobs_running ?? 0} color="#38bdf8" />
            <MetricCard label="Job falliti" value={summary?.jobs_failed ?? 0} color="#f87171" />
            <MetricCard label="Job completati" value={summary?.jobs_completed ?? 0} color="#34d399" />
            <MetricCard label="Outbox pending" value={summary?.outbox_pending ?? 0} color="#fbbf24" />
            <MetricCard label="Outbox failed" value={summary?.outbox_failed ?? 0} color="#f87171" />
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))', gap: '1.5rem' }}>
            <DistributionCard title="Per sorgente" data={summary?.by_source ?? {}} />
            <DistributionCard title="Per tipo media" data={summary?.by_media_type ?? {}} />
          </div>
        </>
      )}
    </div>
  )
}

function MetricCard({ label, value, color }: { label: string; value: number; color: string }) {
  return (
    <div
      style={{
        background: '#1e293b',
        border: '1px solid #334155',
        borderRadius: '8px',
        padding: '1.25rem',
        borderTop: `4px solid ${color}`,
      }}
    >
      <div style={{ fontSize: '0.85rem', color: '#94a3b8', marginBottom: '0.5rem' }}>{label}</div>
      <div style={{ fontSize: '2rem', fontWeight: 700, color: '#e2e8f0' }}>{value.toLocaleString('it-IT')}</div>
    </div>
  )
}

function DistributionCard({ title, data }: { title: string; data: Record<string, number> }) {
  const entries = Object.entries(data).filter(([, v]) => v > 0)
  return (
    <div
      style={{
        background: '#1e293b',
        border: '1px solid #334155',
        borderRadius: '8px',
        padding: '1.5rem',
      }}
    >
      <h3 style={{ margin: '0 0 1rem', fontSize: '1.1rem', color: '#e2e8f0' }}>{title}</h3>
      {entries.length === 0 ? (
        <div style={{ color: '#64748b', fontSize: '0.9rem' }}>Nessun dato disponibile.</div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.6rem' }}>
          {entries.map(([key, value]) => (
            <div key={key} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <span style={{ color: '#94a3b8', fontSize: '0.9rem', textTransform: 'capitalize' }}>{key}</span>
              <span style={{ color: '#e2e8f0', fontWeight: 600 }}>{value.toLocaleString('it-IT')}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
