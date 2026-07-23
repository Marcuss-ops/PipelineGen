import { AssetDetails, VerifyIndexResponse } from '../../../../../api/assetTypes'
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
  const isIndexable = asset.index_state !== 'NOT_INDEXABLE'

  return (
    <div>
      <h3 className={styles.sectionTitle}>Stato indicizzazione</h3>
      <div className={styles.infoGrid}>
        <InfoCard
          label="Indicizzabile"
          value={isIndexable ? 'Sì' : 'No'}
        />
        <InfoCard label="Asset state" value={asset.asset_state} />
        <InfoCard label="Index state" value={asset.index_state} />
        <InfoCard
          label="Index health"
          value={asset.index_health?.label ?? asset.index_state}
        />
        <InfoCard
          label="Embedding SQLite"
          value={asset.has_embedding ? 'Presente' : 'Mancante'}
        />
        <InfoCard label="Modello embedding" value={asset.embedding_version || '-'} />
        <InfoCard label="Collection / versione" value={asset.collection_version || '-'} />
        <InfoCard label="Hash corrente" value={asset.content_hash || '-'} />
        <InfoCard label="Hash indicizzato" value={asset.indexed_content_hash || '-'} />
        <InfoCard
          label="Coerenza hash"
          value={
            asset.content_hash && asset.indexed_content_hash
              ? asset.content_hash === asset.indexed_content_hash
                ? 'OK'
                : 'DIVERGENTE'
              : '-'
          }
        />
        <InfoCard
          label="Outbox pending"
          value={String(asset.pending_outbox_events ?? 0)}
        />
        <InfoCard label="Ultimo errore" value={asset.last_error || 'nessuno'} />
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
            <InfoCard label="Embedding SQLite" value={verifyResult.sqlite.embedding_present ? 'Presente' : 'Mancante'} />
            <InfoCard label="Hash corrente" value={verifyResult.sqlite.content_hash || '-'} />
            <InfoCard label="Hash indicizzato" value={verifyResult.sqlite.indexed_content_hash || '-'} />
            <InfoCard label="Outbox pending" value={String(verifyResult.outbox.pending)} />
            <InfoCard label="Stato payload Qdrant" value={verifyResult.qdrant.payload_lifecycle_state || '-'} />
          </div>
        </div>
      )}
    </div>
  )
}
