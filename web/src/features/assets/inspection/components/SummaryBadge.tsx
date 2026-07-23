import styles from '../AssetInspector.module.css'

interface SummaryBadgeProps {
  label: string
  value?: string
}

export function SummaryBadge({ label, value }: SummaryBadgeProps) {
  if (!value) return null
  return (
    <div className={styles.summaryBadge}>
      <span className={styles.badgeLabel}>{label}:</span>
      <span className={styles.badgeValue}>{value}</span>
    </div>
  )
}
