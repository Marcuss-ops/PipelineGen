import { AssetDetails } from '../../../../../api/assetTypes'
import styles from '../../AssetInspector.module.css'

interface ProcessingTabProps {
  asset: AssetDetails
}

export function ProcessingTab({ asset }: ProcessingTabProps) {
  return (
    <div>
      <h3 className={styles.sectionTitle}>Processing</h3>
      {!asset.processing?.length && <p className={styles.emptyText}>Nessun record di processing.</p>}
      {asset.processing?.map((p, idx) => (
        <div key={idx} className={styles.listItem}>
          <div className={styles.itemTitle}>{p.step}</div>
          <div className={styles.itemMeta}>
            Stato: {p.status} {p.error && `- ${p.error}`}
          </div>
          {p.started_at && <div className={styles.itemMeta}>Iniziato: {p.started_at}</div>}
          {p.completed_at && <div className={styles.itemMeta}>Completato: {p.completed_at}</div>}
        </div>
      ))}
    </div>
  )
}
