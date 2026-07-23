import { useCallback, useEffect, useMemo, useState } from 'react'
import { listAssets, getAssetFacets, AssetSummary, AssetFilter, AssetInventoryFacets } from '../../../api/assets'
import { FilterState } from './types'

const EMPTY_FACETS: AssetInventoryFacets = {
  media_types: [],
  lifecycle_states: [],
  asset_states: [],
  index_states: [],
  sources: [],
  providers: [],
}

export interface UseAssetInventoryResult {
  assets: AssetSummary[]
  loading: boolean
  error: string | null
  hasMore: boolean
  facets: AssetInventoryFacets
  facetsError: string | null
  filters: FilterState
  activeFilterCount: number
  handleFilterChange: (key: keyof FilterState, value: string) => void
  clearFilters: () => void
  handleLoadMore: () => void
  refresh: () => void
}

export function useAssetInventory(): UseAssetInventoryResult {
  const [assets, setAssets] = useState<AssetSummary[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [cursor, setCursor] = useState<string>('')
  const [hasMore, setHasMore] = useState(false)
  const [facets, setFacets] = useState<AssetInventoryFacets>(EMPTY_FACETS)
  const [facetsError, setFacetsError] = useState<string | null>(null)
  const [filters, setFilters] = useState<FilterState>({
    search: '',
    source: '',
    media_type: '',
    lifecycle_state: '',
    category: '',
  })

  const loadAssets = useCallback(async (nextCursor?: string, currentFilters = filters) => {
    setLoading(true)
    setError(null)
    try {
      const apiFilters: AssetFilter = {
        source: currentFilters.source,
        media_type: currentFilters.media_type,
        lifecycle_state: currentFilters.lifecycle_state,
        category: currentFilters.category,
        search: currentFilters.search,
        cursor: nextCursor,
        limit: 25,
      }
      const data = await listAssets(apiFilters)
      setAssets((prev) => (nextCursor ? [...prev, ...data.items] : data.items))
      setCursor(data.next_cursor)
      setHasMore(data.has_more)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Errore sconosciuto')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    getAssetFacets()
      .then((data) => {
        if (!cancelled) setFacets(data)
      })
      .catch((err) => {
        if (!cancelled) setFacetsError(err instanceof Error ? err.message : 'Errore caricamento filtri')
      })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    const timeout = setTimeout(() => {
      loadAssets(undefined)
    }, 300)
    return () => clearTimeout(timeout)
  }, [filters.search, filters.source, filters.media_type, filters.lifecycle_state, filters.category, loadAssets])

  const handleFilterChange = useCallback((key: keyof FilterState, value: string) => {
    setFilters((prev) => ({ ...prev, [key]: value }))
  }, [])

  const clearFilters = useCallback(() => {
    setFilters({
      search: '',
      source: '',
      media_type: '',
      lifecycle_state: '',
      category: '',
    })
  }, [])

  const handleLoadMore = useCallback(() => {
    if (hasMore && !loading) {
      loadAssets(cursor)
    }
  }, [hasMore, loading, cursor, loadAssets])

  const refresh = useCallback(() => {
    loadAssets(undefined)
  }, [loadAssets])

  const activeFilterCount = useMemo(() => {
    return Object.values(filters).filter((v) => v !== '').length
  }, [filters])

  return {
    assets,
    loading,
    error,
    hasMore,
    facets,
    facetsError,
    filters,
    activeFilterCount,
    handleFilterChange,
    clearFilters,
    handleLoadMore,
    refresh,
  }
}
