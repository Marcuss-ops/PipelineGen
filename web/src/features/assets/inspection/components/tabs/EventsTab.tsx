import { AssetDetails, AssetProcessing, OutboxEventProjection } from '../../../../../api/assetTypes'
import styles from '../../AssetInspector.module.css'

// The admin console has no global SSE endpoint. This timeline is built from
// the resource-specific REST projections returned with the asset detail.

interface EventsTabProps {
  asset: AssetDetails
}

interface TimelineEntry {
  id: string
  type: 'processing' | 'outbox' | 'error' | 'update'
  timestamp: string
  title: string
  meta: string
  severity?: 'neutral' | 'info' | 'warning' | 'error' | 'success'
}

export function EventsTab({ asset }: EventsTabProps) {
  const entries: TimelineEntry[] = []

  asset.processing?.forEach((p, idx) => {
    entries.push({
      id: `p-${idx}`,
      type: 'processing',
      timestamp: p.completed_at || p.started_at || '',
      title: `Processing: ${p.step}`,
      meta: `stato ${p.status}${p.error_message ? ` · errore: ${p.error_message}` : ''}${p.attempt_count ? ` · tentativi: ${p.attempt_count}` : ''}`,
      severity: p.status === 'completed' ? 'success' : p.error_message ? 'error' : 'info',
    })
  })

  asset.outbox_events?.forEach((ev, idx) => {
    entries.push({
      id: `o-${idx}`,
      type: 'outbox',
      timestamp: ev.updated_at,
      title: `Outbox: ${ev.event_type}`,
      meta: `chiave ${ev.event_key} · stato ${ev.status}${ev.last_error ? ` · errore: ${ev.last_error}` : ''} · tentativi: ${ev.attempt_count}`,
      severity: ev.status === 'completed' ? 'success' : ev.last_error ? 'error' : 'warning',
    })
  })

  if (asset.last_error) {
    entries.push({
      id: 'last-error',
      type: 'error',
      timestamp: asset.updated_at,
      title: 'Ultimo errore',
      meta: asset.last_error,
      severity: 'error',
    })
  }

  entries.sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime())

  return (
    <div>
      <h3 className={styles.sectionTitle}>Eventi</h3>
      {!entries.length && <p className={styles.emptyText}>Nessun evento disponibile.</p>}
      {entries.map((e) => (
        <div key={e.id} className={styles.listItem} style={{ borderLeft: `4px solid ${severityColor(e.severity)}` }}>
          <div className={styles.itemTitle}>{e.title}</div>
          <div className={styles.itemMeta}>{e.meta}</div>
          {e.timestamp && (
            <div className={styles.itemMeta}>
              {new Date(e.timestamp).toLocaleString('it-IT')}
            </div>
          )}
        </div>
      ))}
    </div>
  )
}

function severityColor(severity?: string) {
  switch (severity) {
    case 'success': return '#34d399'
    case 'error': return '#f87171'
    case 'warning': return '#fbbf24'
    case 'info': return '#38bdf8'
    default: return '#64748b'
  }
}
