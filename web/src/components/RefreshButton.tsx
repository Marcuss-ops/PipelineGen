export interface RefreshButtonProps {
  onClick: () => void
  label?: string
}

export default function RefreshButton({ onClick, label = 'Aggiorna' }: RefreshButtonProps) {
  return (
    <button
      onClick={onClick}
      style={{
        padding: '0.55rem 1rem',
        background: '#1e293b',
        color: '#e2e8f0',
        border: '1px solid #334155',
        borderRadius: '6px',
        cursor: 'pointer',
        fontSize: '0.85rem',
        fontWeight: 500,
      }}
    >
      {label}
    </button>
  )
}
