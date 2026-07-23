import { AssetDetails } from '../../../../../api/assetTypes'
import styles from '../../AssetInspector.module.css'
import { InfoCard } from '../InfoCard'

interface OverviewTabProps {
  asset: AssetDetails
}

export function GeneralTab({ asset }: OverviewTabProps) {
  const storageParts: string[] = []
  if (asset.has_local_file) storageParts.push('Locale')
  if (asset.has_drive_file) storageParts.push('Drive')
  const storageLabel = storageParts.length > 0 ? storageParts.join(' + ') : 'mancante'

  return (
    <div>
      <h3 className={styles.sectionTitle}>Panoramica</h3>
      <div className={styles.infoGrid}>
        <InfoCard label="Nome" value={asset.name || asset.filename} />
        <InfoCard label="ID" value={asset.id} />
        <InfoCard label="Media type" value={asset.media_type} />
        <InfoCard label="Source" value={asset.source} />
        <InfoCard label="Provider" value={asset.provider} />
        <InfoCard label="Categoria" value={asset.category || '-'} />
        <InfoCard label="Gruppo" value={asset.group || '-'} />
        <InfoCard label="Lifecycle" value={asset.lifecycle_state} />
        <InfoCard label="Journey (asset_state)" value={asset.asset_state} />
        <InfoCard label="Index state" value={asset.index_state} />
        <InfoCard
          label="Index health"
          value={asset.index_health?.label ?? asset.index_state}
        />
        <InfoCard label="Storage" value={storageLabel} />
        <InfoCard label="Outbox pending" value={String(asset.pending_outbox_events ?? 0)} />
        <InfoCard label="Ultimo errore" value={asset.last_error || 'nessuno'} />
        <InfoCard
          label="Creato"
          value={asset.created_at ? new Date(asset.created_at).toLocaleString('it-IT') : '-'}
        />
        <InfoCard
          label="Aggiornato"
          value={asset.updated_at ? new Date(asset.updated_at).toLocaleString('it-IT') : '-'}
        />
      </div>
    </div>
  )
}
