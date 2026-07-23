import { AssetDetails, VerifyIndexResponse } from '../../../../../api/client'
import styles from '../../AssetInspector.module.css'
import { InfoCard } from '../InfoCard'

interface IndexingTabProps {
  asset: AssetDetails
  onVerify: () => void
  onReindex: () => void
  verifyResult: VerifyIndexResponse | null
  verifyLoading: boolean
  reindexLoading: boolean
}

export function IndexingTab({
  asset,
  onVerify,
  onReindex,
  verifyResult,
  verifyLoading,
  reindexLoading,
}: IndexingTabProps) {
  return (
    <div>
      <h3 className={styles.sectionTitle}>Stato indicizzazione</h3>
      <div className={styles.infoGrid}>
        <InfoCard label="SQLite" value={asset.lifecycle_state ? 'presente' : '-'} />
        <InfoCard
          label="Embedding"
          value={
            asset.embedding_info?.present
              ? `${asset.embedding_info.dimensions}d (${asset.embedding_info.version})`
              : 'mancante'
          }
        />
        <InfoCard label="Modello" value={asset.embedding_info?.version || '-'} />
        <InfoCard
          label="Dimensioni"
          value={asset.embedding_info?.dimensions ? String(asset.embedding_info.dimensions) : '-'}
        />
      </div>
      <div className={styles.buttonRow}>
        <button
          onClick={onReindex}
          disabled={reindexLoading}
          className={reindexLoading ? styles.disabledButton : styles.secondaryButton}
        >
          {reindexLoading ? 'Reindicizzazione...' : 'Reindicizza'}
        </button>
        <button
          onClick={onVerify}
          disabled={verifyLoading}
          className={verifyLoading ? styles.disabledButton : styles.secondaryButton}
        >
          {verifyLoading ? 'Verifica in corso...' : 'Verifica Qdrant'}
        </button>
      </div>
      {verifyResult && (
        <div className={styles.listItem}>
          <h4 className={styles.sectionTitle}>Risultato verifica Qdrant</h4>
          <div className={styles.infoGrid}>
            <InfoCard label="Coerente" value={verifyResult.consistent ? 'Sì' : 'No'} />
            <InfoCard label="Point presente" value={verifyResult.qdrant.point_present ? 'Sì' : 'No'} />
            <InfoCard label="Collection" value={verifyResult.qdrant.collection || '-'} />
            <InfoCard label="Dimensioni vettore" value={String(verifyResult.qdrant.vector_dimensions ?? '-')} />
            <InfoCard label="Hash corrente" value={verifyResult.sqlite.content_hash || '-'} />
            <InfoCard label="Hash indicizzato" value={verifyResult.sqlite.indexed_content_hash || '-'} />
            <InfoCard label="Embedding SQLite" value={verifyResult.sqlite.embedding_present ? 'Presente' : 'Mancante'} />
            <InfoCard label="Outbox pending" value={String(verifyResult.outbox.pending)} />
          </div>
        </div>
      )}
    </div>
  )
}
