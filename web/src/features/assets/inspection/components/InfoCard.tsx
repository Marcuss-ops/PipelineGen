import styles from '../AssetInspector.module.css'

interface InfoCardProps {
  label: string
  value: string
}

export function InfoCard({ label, value }: InfoCardProps) {
  return (
    <div className={styles.infoCard}>
      <div className={styles.infoCardLabel}>{label}</div>
      <div className={styles.infoCardValue}>{value}</div>
    </div>
  )
}
