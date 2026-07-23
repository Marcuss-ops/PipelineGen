import { primaryButtonStyle, secondaryButtonStyle } from './styles'

interface AssetBulkToolbarProps {
  selectedCount: number
  onClear: () => void
  onOpen: () => void
}

export function AssetBulkToolbar({ selectedCount, onClear, onOpen }: AssetBulkToolbarProps) {
  if (selectedCount === 0) return null

  return (
    <div
      style={{
        background: 'rgba(56,189,248,0.1)',
        border: '1px solid #38bdf8',
        borderRadius: '8px',
        padding: '1rem',
        marginBottom: '1.5rem',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: '1rem',
        flexWrap: 'wrap',
      }}
    >
      <div style={{ color: '#e2e8f0', fontWeight: 500 }}>
        {selectedCount} {selectedCount === 1 ? 'asset selezionato' : 'asset selezionati'}
      </div>
      <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap' }}>
        <button onClick={onClear} style={secondaryButtonStyle}>
          Deseleziona
        </button>
        <button onClick={onOpen} style={primaryButtonStyle}>
          Azioni bulk →
        </button>
      </div>
    </div>
  )
}
