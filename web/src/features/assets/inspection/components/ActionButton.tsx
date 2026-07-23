import styles from '../AssetInspector.module.css'

interface ActionButtonProps {
  label: string
  url?: string
  onClick: (url?: string) => void
}

export function ActionButton({ label, url, onClick }: ActionButtonProps) {
  const disabled = !url
  return (
    <button
      onClick={() => onClick(url)}
      disabled={disabled}
      className={disabled ? styles.disabledButton : styles.secondaryButton}
    >
      {label}
    </button>
  )
}
