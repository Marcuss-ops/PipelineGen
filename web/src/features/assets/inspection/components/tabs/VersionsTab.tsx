import { AssetDetails } from '../../../../../api/assetTypes'
import styles from '../../AssetInspector.module.css'

interface VersionsTabProps {
  asset: AssetDetails
}

export function VersionsTab({ asset }: VersionsTabProps) {
  return (
    <div>
      <h3 className={styles.sectionTitle}>Versioni</h3>
      {!asset.versions?.length && <p className={styles.emptyText}>Nessuna versione archiviata.</p>}
      {asset.versions?.map((v, idx) => (
        <div key={idx} className={styles.listItem}>
          <div className={styles.itemTitle}>Versione {v.version_number}</div>
          <div className={styles.itemMeta}>
            {v.file_hash && <span>Hash: {v.file_hash} </span>}
            {v.file_size !== undefined && <span>Size: {v.file_size} bytes </span>}
            {v.mime_type && <span>MIME: {v.mime_type}</span>}
          </div>
          {v.created_at && <div className={styles.itemMeta}>{v.created_at}</div>}
        </div>
      ))}
    </div>
  )
}
