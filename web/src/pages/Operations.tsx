import { usePollingQuery } from '../hooks/usePollingQuery'
import RefreshButton from '../components/RefreshButton'
import {
  getHealth,
  getReady,
  getModels,
  getQdrantReady,
  getMediaIndexHealth,
  getOperationsErrors,
} from '../api/operations'
import { getOutboxStatus } from '../api/outbox'
import {
  DriveCard,
  ErrorsCard,
  HealthCard,
  IndexingCard,
  ModelsCard,
  OutboxCard,
  QdrantCard,
  SectionState,
} from './OperationsCards'

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
    wrap(getHealth(true)), wrap(getReady()), wrap(getModels()), wrap(getQdrantReady()),
    wrap(getMediaIndexHealth()), wrap(getOutboxStatus()), wrap(getOperationsErrors()),
  ])
  return { health, ready, models, qdrant, indexing, outbox, errors }
}

export default function Operations() {
  const { data, loading, error, refresh } = usePollingQuery<OperationsData>({ queryFn: fetchOperations, interval: 10000 })
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
        <div><h2 style={{ margin: 0, fontSize: '1.75rem', color: '#e2e8f0' }}>Operations</h2><p style={{ margin: '0.5rem 0 0', color: '#94a3b8' }}>Health, modelli, Qdrant, Drive, indicizzazione, outbox ed errori operativi.</p></div>
        <RefreshButton onClick={refresh} />
      </div>
      {isLoading ? <div style={{ color: '#94a3b8', textAlign: 'center', padding: '2rem' }}>Caricamento...</div> : sectionError ? <div style={{ background: 'rgba(248,113,113,0.1)', border: '1px solid #f87171', color: '#f87171', padding: '1rem', borderRadius: '8px' }}>{sectionError}</div> : <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(360px, 1fr))', gap: '1.5rem' }}><HealthCard title="Health" state={health} ready={ready} /><ModelsCard state={models} /><QdrantCard state={qdrant} /><DriveCard state={health} /><IndexingCard state={indexing} /><OutboxCard state={outbox} /><ErrorsCard state={errors} /></div>}
    </div>
  )
}
