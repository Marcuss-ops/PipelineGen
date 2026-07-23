import { AssetActionsResponse } from '../../../../../api/assetTypes'
import styles from '../../AssetInspector.module.css'
import { ActionButton } from '../ActionButton'

interface EventsTabProps {
  actions: AssetActionsResponse | null
  onAction: (url?: string) => void
  onUpdate: () => void
}

export function EventsTab({ actions, onAction, onUpdate }: EventsTabProps) {
  return (
    <div>
      <h3 className={styles.sectionTitle}>Azioni</h3>
      <div className={styles.buttonRow}>
        <button onClick={onUpdate} className={styles.primaryButton}>
          💾 Update clip (salva modifiche)
        </button>
      </div>
      {!actions?.is_clip_source && (
        <p className={styles.emptyText}>Azioni avanzate disponibili solo per asset clip.</p>
      )}
      <div className={styles.buttonRow}>
        <ActionButton label="Reindicizza" url={actions?.reindex} onClick={onAction} />
        <ActionButton label="Verifica" url={actions?.verify} onClick={onAction} />
        <ActionButton label="Riprocessa" url={actions?.reprocess} onClick={onAction} />
        <ActionButton label="Ricarica su Drive" url={actions?.reupload} onClick={onAction} />
        <ActionButton label="Correggi hash" url={actions?.fix_hash} onClick={onAction} />
        <ActionButton label="Riconcilia" url={actions?.reconcile} onClick={onAction} />
      </div>
    </div>
  )
}
