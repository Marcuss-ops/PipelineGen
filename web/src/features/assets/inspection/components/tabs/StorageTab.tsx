import { AssetDetails } from '../../../../../api/assetTypes'
import styles from '../../AssetInspector.module.css'
import { InfoCard } from '../InfoCard'

interface StorageTabProps {
  asset: AssetDetails
}

export function StorageTab({ asset }: StorageTabProps) {
  const local = asset.locations?.find((l) => l.location_kind === 'local')
  const drive = asset.locations?.find((l) => l.location_kind === 'drive')
  const primary = asset.locations?.find((l) => l.is_primary)

  function formatBytes(n?: number) {
    if (n === undefined || n === null) return '-'
    if (n === 0) return '0 B'
    const k = 1024
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(n) / Math.log(k))
    return `${parseFloat((n / k ** i).toFixed(2))} ${sizes[i]}`
  }

  return (
    <div>
      <h3 className={styles.sectionTitle}>Storage</h3>
      <div className={styles.infoGrid}>
        <InfoCard label="Locale" value={asset.has_local_file ? (local ? 'presente' : 'presente') : 'mancante'} />
        <InfoCard label="Drive" value={asset.has_drive_file ? (drive ? 'presente' : 'presente') : 'mancante'} />
        <InfoCard label="Primary location" value={primary?.location_kind ?? '-'} />
        <InfoCard label="File hash" value={primary?.file_hash ?? '-'} />
        <InfoCard label="File size" value={formatBytes(primary?.file_size_bytes)} />
        <InfoCard label="MIME type" value={primary?.mime_type ?? '-'} />
        <InfoCard label="Drive ID" value={drive?.external_id ?? '-'} />
      </div>

      <h4 className={styles.sectionTitle}>Posizioni</h4>
      {!asset.locations?.length && (
        <p className={styles.emptyText}>Nessuna posizione disponibile.</p>
      )}
      {asset.locations?.map((loc) => (
        <div key={loc.id} className={styles.listItem}>
          <div className={styles.itemTitle}>
            {loc.location_kind} {loc.is_primary && '(primary)'}
          </div>
          <div className={styles.itemMeta} style={{ wordBreak: 'break-all' }}>
            {loc.uri}
          </div>
          <div className={styles.itemMeta}>
            {loc.external_id && <span>ID: {loc.external_id} · </span>}
            {loc.file_hash && <span>Hash: {loc.file_hash} · </span>}
            {loc.file_size_bytes !== undefined && loc.file_size_bytes > 0 && (
              <span>Size: {formatBytes(loc.file_size_bytes)} · </span>
            )}
            {loc.mime_type && <span>MIME: {loc.mime_type}</span>}
          </div>
        </div>
      ))}
    </div>
  )
}
