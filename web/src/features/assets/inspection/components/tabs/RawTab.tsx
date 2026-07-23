import { AssetDetails } from '../../../../../api/assetTypes'
import styles from '../../AssetInspector.module.css'

interface RawTabProps {
  asset: AssetDetails
}

export function RawTab({ asset }: RawTabProps) {
  return (
    <div>
      <h3 className={styles.sectionTitle}>Raw JSON</h3>
      <pre className={styles.rawJson}>{JSON.stringify(asset, null, 2)}</pre>
    </div>
  )
}
