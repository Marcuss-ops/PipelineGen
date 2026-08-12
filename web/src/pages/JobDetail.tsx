import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { usePollingQuery } from '../hooks/usePollingQuery'
import RefreshButton from '../components/RefreshButton'
import { getJobFull, JobFull, cancelJob, retryJob } from '../api/jobs'
import {
  ArtifactsTab,
  buttonStyle,
  ErrorsTab,
  JobDetailBadge,
  JsonTab,
  RawJsonTab,
  SummaryTab,
  tabButtonStyle,
  TimelineTab,
} from './JobDetailPanels'

export default function JobDetail() {
  const { id } = useParams<{ id: string }>()
  const [activeTab, setActiveTab] = useState<'summary' | 'timeline' | 'input' | 'output' | 'errors' | 'artifacts' | 'raw'>('summary')
  const [actionLoading, setActionLoading] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [pausePolling, setPausePolling] = useState(false)

  const { data: job, loading, error, refresh } = usePollingQuery<JobFull>({
    queryFn: async () => {
      if (!id) throw new Error('ID mancante')
      return getJobFull(id)
    },
    interval: 1500,
    enabled: !!id,
    pause: pausePolling,
  })

  useEffect(() => {
    setPausePolling(job?.status !== 'RUNNING')
  }, [job])

  const handleCancel = async () => {
    if (!id) return
    setActionLoading('cancel')
    setActionError(null)
    try {
      await cancelJob(id)
      refresh()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Errore cancellazione')
    } finally {
      setActionLoading(null)
    }
  }

  const handleRetry = async () => {
    if (!id) return
    setActionLoading('retry')
    setActionError(null)
    try {
      await retryJob(id)
      refresh()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Errore retry')
    } finally {
      setActionLoading(null)
    }
  }

  if (loading) return <div style={{ padding: '2rem', color: '#94a3b8', textAlign: 'center' }}>Caricamento job...</div>

  if (error || !job) {
    return <div style={{ padding: '2rem' }}><div style={{ background: 'rgba(248,113,113,0.1)', border: '1px solid #f87171', color: '#f87171', padding: '1rem', borderRadius: '8px', marginBottom: '1rem' }}>{error || 'Job non trovato'}</div><Link to="/jobs" style={{ color: '#38bdf8', textDecoration: 'none' }}>← Torna ai Jobs</Link></div>
  }

  const isScriptJob = job.type && (job.type.includes('script') || job.type.includes('generate'))
  const tabs = [
    { key: 'summary', label: 'Riepilogo' }, { key: 'timeline', label: 'Timeline eventi' }, { key: 'input', label: 'Input' },
    { key: 'output', label: 'Output' }, { key: 'errors', label: 'Errori' }, { key: 'artifacts', label: 'Artifact' }, { key: 'raw', label: 'Raw JSON' },
  ] as { key: typeof activeTab; label: string }[]

  return (
    <div style={{ padding: '2rem' }}>
      <div style={{ marginBottom: '1.5rem' }}><Link to="/jobs" style={{ color: '#38bdf8', textDecoration: 'none', fontSize: '0.9rem' }}>← Torna ai Jobs</Link></div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', flexWrap: 'wrap', gap: '1rem', marginBottom: '1.5rem' }}>
        <div>
          <h2 style={{ margin: '0 0 0.5rem', fontSize: '1.75rem', color: '#e2e8f0' }}>Job {job.id.slice(0, 16)}...</h2>
          <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap' }}>
            <JobDetailBadge label="Tipo" value={job.type} /><JobDetailBadge label="Stato" value={job.status} /><JobDetailBadge label="Stage" value={job.current_stage || job.current_step} /><JobDetailBadge label="Progresso" value={`${job.progress || 0}%`} />
            {job.retryable !== undefined && <JobDetailBadge label="Retryable" value={job.retryable ? 'Sì' : 'No'} />}
          </div>
        </div>
        <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
          <RefreshButton onClick={refresh} />
          {isScriptJob && <Link to={`/scripts/${encodeURIComponent(job.id)}`} style={{ ...buttonStyle, background: '#818cf8' }}>Apri Script</Link>}
          <button onClick={handleCancel} disabled={actionLoading === 'cancel'} style={buttonStyle}>{actionLoading === 'cancel' ? '...' : 'Cancella'}</button>
          <button onClick={handleRetry} disabled={actionLoading === 'retry'} style={buttonStyle}>{actionLoading === 'retry' ? '...' : 'Retry'}</button>
        </div>
      </div>
      {(job.error || actionError) && <div style={{ background: 'rgba(248,113,113,0.1)', border: '1px solid #f87171', color: '#f87171', padding: '1rem', borderRadius: '8px', marginBottom: '1.5rem' }}>{job.error && <div><strong>Errore job:</strong> {job.error}</div>}{actionError && <div><strong>Errore azione:</strong> {actionError}</div>}</div>}
      <div style={{ display: 'flex', gap: '0.5rem', borderBottom: '1px solid #334155', marginBottom: '1.5rem' }}>
        {tabs.map((tab) => <button key={tab.key} onClick={() => setActiveTab(tab.key)} style={{ ...tabButtonStyle, borderBottom: activeTab === tab.key ? '2px solid #38bdf8' : '2px solid transparent', background: activeTab === tab.key ? '#0f172a' : 'transparent', color: activeTab === tab.key ? '#38bdf8' : '#94a3b8' }}>{tab.label}</button>)}
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

function extractPayload(job: JobFull): unknown {
  if (!job.job) return undefined
  const jobData = job.job as Record<string, unknown>
  return jobData.payload ?? jobData.Payload ?? undefined
}
