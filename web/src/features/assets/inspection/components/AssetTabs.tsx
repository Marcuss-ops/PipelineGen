import { TabKey } from '../types'
import styles from '../AssetInspector.module.css'

interface AssetTabsProps {
  activeTab: TabKey
  onTabChange: (tab: TabKey) => void
}

const TABS: { key: TabKey; label: string }[] = [
  { key: 'panoramica', label: 'Panoramica' },
  { key: 'pipeline', label: 'Pipeline' },
  { key: 'indicizzazione', label: 'Indicizzazione' },
  { key: 'storage', label: 'Storage' },
  { key: 'eventi', label: 'Eventi' },
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
