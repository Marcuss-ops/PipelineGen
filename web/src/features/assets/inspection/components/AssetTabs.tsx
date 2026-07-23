import { TabKey } from '../types'
import styles from '../AssetInspector.module.css'

interface AssetTabsProps {
  activeTab: TabKey
  onTabChange: (tab: TabKey) => void
}

const TABS: { key: TabKey; label: string }[] = [
  { key: 'generale', label: 'Generale' },
  { key: 'metadata', label: 'Metadata' },
  { key: 'indicizzazione', label: 'Indicizzazione' },
  { key: 'files', label: 'File e posizioni' },
  { key: 'processing', label: 'Processing' },
  { key: 'versions', label: 'Versioni' },
  { key: 'azioni', label: 'Azioni' },
  { key: 'raw', label: 'Raw JSON' },
  { key: 'audit', label: 'Audit' },
]

export function AssetTabs({ activeTab, onTabChange }: AssetTabsProps) {
  return (
    <div className={styles.tabBar}>
      {TABS.map((t) => (
        <button
          key={t.key}
          onClick={() => onTabChange(t.key)}
          className={activeTab === t.key ? styles.tabButtonActive : styles.tabButton}
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}
