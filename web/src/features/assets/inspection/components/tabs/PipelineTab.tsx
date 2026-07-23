import { AssetDetails } from '../../../../../api/assetTypes'
import styles from '../../AssetInspector.module.css'

interface PipelineTabProps {
  asset: AssetDetails
}

const PIPELINE_STEPS: { code: string; label: string }[] = [
  { code: 'DISCOVERED', label: 'Scoperto' },
  { code: 'DOWNLOADED', label: 'Scaricato' },
  { code: 'NORMALIZED', label: 'Normalizzato' },
  { code: 'HASHED', label: 'Hash calcolato' },
  { code: 'UPLOADED', label: 'Caricato' },
  { code: 'TRANSCRIBED', label: 'Trascritto' },
  { code: 'ENRICHED', label: 'Arricchito' },
  { code: 'TRANSLATED', label: 'Tradotto' },
  { code: 'INDEX_PENDING', label: 'Indicizzazione in attesa' },
  { code: 'INDEXED', label: 'Indicizzato' },
  { code: 'READY', label: 'Pronto' },
  { code: 'READY_MULTILINGUAL', label: 'Pronto multilingua' },
]

export function PipelineTab({ asset }: PipelineTabProps) {
  const current = asset.asset_state ?? ''
  const currentIndex = PIPELINE_STEPS.findIndex((s) => s.code === current)

  return (
    <div>
      <h3 className={styles.sectionTitle}>Pipeline</h3>
      <div className={styles.cardList}>
        {PIPELINE_STEPS.map((step, idx) => {
          const reached = currentIndex >= idx
          const isCurrent = current === step.code
          return (
            <div
              key={step.code}
              className={styles.listItem}
              style={{
                opacity: reached ? 1 : 0.55,
                borderLeft: isCurrent ? '4px solid #38bdf8' : '4px solid transparent',
              }}
            >
              <div className={styles.itemTitle}>
                {reached ? '✓' : '○'} {step.label}
                {isCurrent && <span style={{ marginLeft: '0.5rem', color: '#38bdf8' }}>(corrente)</span>}
              </div>
              <div className={styles.itemMeta}>{step.code}</div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
