import { primaryButtonStyle } from './styles'

interface AssetPaginationProps {
  hasMore: boolean
  loading: boolean
  onLoadMore: () => void
}

export function AssetPagination({ hasMore, loading, onLoadMore }: AssetPaginationProps) {
  if (!hasMore) return null

  return (
    <div style={{ textAlign: 'center', marginTop: '1.5rem' }}>
      <button onClick={onLoadMore} disabled={loading} style={primaryButtonStyle}>
        {loading ? 'Caricamento...' : 'Carica altri'}
      </button>
    </div>
  )
}
