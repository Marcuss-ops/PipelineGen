
import { AssetDetails } from '../../../../api/client'
import AssetPreview from '../../../../components/AssetPreview'
import styles from '../AssetInspector.module.css'
import { PreviewButton } from './PreviewButton'
import { SummaryBadge } from './SummaryBadge'

interface AssetHeaderProps {
  asset: AssetDetails
}

export function AssetHeader({ asset }: AssetHeaderProps) {
  return (
    <div className={styles.headerCard}>
      <div className={styles.headerPreview}>
        <AssetPreview
          id={asset.id}
          mediaType={asset.media_type}
          thumbnailUrl={asset.thumbnail_url}
          name={asset.name}
          size={160}
        />
      </div>
      <div className={styles.headerMeta}>
        <h2 className={styles.assetTitle}>{asset.name || asset.filename}</h2>
        <div className={styles.badgeList}>
          <SummaryBadge label="ID" value={asset.id} />
          <SummaryBadge label="Tipo" value={asset.media_type} />
          <SummaryBadge label="Sorgente" value={asset.source} />
          <SummaryBadge label="Stato" value={asset.lifecycle_state} />
          <SummaryBadge label="Categoria" value={asset.category} />
        </div>
        <div className={styles.metaLine}>
          {asset.duration && <span>Durata: {asset.duration}</span>}
          {asset.duration_secs !== undefined && <span>({asset.duration_secs} s)</span>}
          {asset.created_at && <span>Creato: {new Date(asset.created_at).toLocaleString('it-IT')}</span>}
        </div>
        <div className={styles.headerActions}>
          <PreviewButton id={asset.id} mediaType={asset.media_type} />
          {asset.source_url && (
            <a
              href={asset.source_url}
              target="_blank"
              rel="noopener noreferrer"
              className={styles.secondaryButton}
            >
              Source URL
            </a>
          )}
        </div>
      </div>
    </div>
  )
}
