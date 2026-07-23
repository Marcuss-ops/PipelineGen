import { useMemo } from 'react'
import { AssetDetails } from '../../../../../api/client'
import styles from '../../AssetInspector.module.css'

interface PipelineTabProps {
  asset: AssetDetails
}

export function PipelineTab({ asset }: PipelineTabProps) {
  const entries = useMemo(() => {
    if (!asset.metadata || typeof asset.metadata !== 'object') return []
    return Object.entries(asset.metadata)
  }, [asset.metadata])

  return (
    <div>
      <h3 className={styles.sectionTitle}>Metadata</h3>
      {entries.length === 0 ? (
        <p className={styles.emptyText}>Nessun metadata disponibile.</p>
      ) : (
        <div className={styles.cardList}>
          {entries.map(([k, v]) => (
            <div key={k} className={styles.listItem}>
              <div className={styles.itemMeta}>{k}</div>
              <div className={styles.itemTitle}>
                {typeof v === 'string' ? v : JSON.stringify(v)}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
