import { useCallback, useState } from 'react'
import { AssetSummary } from '../../../api/assets'

export interface UseAssetSelectionResult {
  selected: Set<string>
  toggleSelect: (id: string) => void
  toggleSelectAll: (assets: AssetSummary[]) => void
  clearSelection: () => void
}

export function useAssetSelection(): UseAssetSelectionResult {
  const [selected, setSelected] = useState<Set<string>>(new Set())

  const toggleSelect = useCallback((id: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }, [])

  const toggleSelectAll = useCallback((assets: AssetSummary[]) => {
    setSelected((prev) => {
      if (prev.size === assets.length && assets.length > 0) {
        return new Set()
      }
      return new Set(assets.map((a) => a.id))
    })
  }, [])

  const clearSelection = useCallback(() => {
    setSelected(new Set())
  }, [])

  return { selected, toggleSelect, toggleSelectAll, clearSelection }
}
