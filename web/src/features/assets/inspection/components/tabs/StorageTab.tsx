import { AssetActionsResponse, AssetDetails } from '../../../../../api/assetTypes'
import styles from '../../AssetInspector.module.css'
import { ActionButton } from '../ActionButton'

interface StorageTabProps {
  asset: AssetDetails
  actions: AssetActionsResponse | null
  onAction: (url?: string) => void
}

export function StorageTab({ asset, actions, onAction }: StorageTabProps) {
  return (
    <div>
      <h3 className={styles.sectionTitle}>File e posizioni</h3>
      {!asset.locations?.length && <p className={styles.emptyText}>Nessuna posizione disponibile.</p>}
      {asset.locations?.map((loc, idx) => (
        <div key={idx} className={styles.listItem}>
          <div className={styles.itemTitle}>{loc.kind}</div>
          <div className={styles.itemMeta} style={{ wordBreak: 'break-all' }}>
            {loc.uri}
          </div>
          <div className={styles.itemMeta}>
            {loc.external_id && <span>ID: {loc.external_id} </span>}
            {loc.file_hash && <span>Hash: {loc.file_hash} </span>}
            {loc.file_size_bytes !== undefined && <span>Size: {loc.file_size_bytes} bytes</span>}
          </div>
        </div>
      ))}
      <div className={styles.buttonRow}>
        <ActionButton label="Verifica hash" url={actions?.verify} onClick={onAction} />
        <ActionButton label="Correggi hash" url={actions?.fix_hash} onClick={onAction} />
        <ActionButton label="Ricarica" url={actions?.reupload} onClick={onAction} />
        <ActionButton label="Riconcilia" url={actions?.reconcile} onClick={onAction} />
      </div>
    </div>
  )
}
