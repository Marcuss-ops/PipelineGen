import { useCallback, useMemo, useState } from 'react'
import { bulkAssets, BulkAction, BulkOperationResponse } from '../../../api/assets'

export interface UseAssetBulkActionsResult {
  bulkAction: BulkAction | ''
  bulkPayloadValue: string
  bulkReviewStatus: string
  bulkLoading: boolean
  bulkPreview: BulkOperationResponse | null
  bulkError: string | null
  bulkSuccess: string | null
  showBulkModal: boolean
  needsPayloadInput: boolean
  openBulkModal: () => void
  closeBulkModal: () => void
  setBulkAction: (action: BulkAction | '') => void
  setBulkPayloadValue: (value: string) => void
  setBulkReviewStatus: (value: string) => void
  canPreview: (selected: Set<string>) => boolean
  runBulk: (dryRun: boolean, selected: Set<string>) => Promise<void>
}

export function useAssetBulkActions(): UseAssetBulkActionsResult {
  const [bulkAction, setBulkAction] = useState<BulkAction | ''>('')
  const [bulkPayloadValue, setBulkPayloadValue] = useState('')
  const [bulkReviewStatus, setBulkReviewStatus] = useState('pending')
  const [bulkLoading, setBulkLoading] = useState(false)
  const [bulkPreview, setBulkPreview] = useState<BulkOperationResponse | null>(null)
  const [bulkError, setBulkError] = useState<string | null>(null)
  const [bulkSuccess, setBulkSuccess] = useState<string | null>(null)
  const [showBulkModal, setShowBulkModal] = useState(false)

  const resetBulk = useCallback(() => {
    setBulkAction('')
    setBulkPayloadValue('')
    setBulkReviewStatus('pending')
    setBulkPreview(null)
    setBulkError(null)
    setBulkSuccess(null)
  }, [])

  const openBulkModal = useCallback(() => {
    resetBulk()
    setShowBulkModal(true)
  }, [resetBulk])

  const closeBulkModal = useCallback(() => {
    setShowBulkModal(false)
    resetBulk()
  }, [resetBulk])

  const buildBulkPayload = useCallback((): Record<string, unknown> => {
    switch (bulkAction) {
      case 'add_tags':
      case 'remove_tags':
        return { tags: bulkPayloadValue.split(',').map((t) => t.trim()).filter(Boolean) }
      case 'set_category':
        return { category: bulkPayloadValue.trim() }
      case 'set_review_status':
        return { review_status: bulkReviewStatus }
      default:
        return {}
    }
  }, [bulkAction, bulkPayloadValue, bulkReviewStatus])

  const canPreview = useCallback((selected: Set<string>) => {
    if (selected.size === 0) return false
    if (!bulkAction) return false
    if (bulkAction === 'add_tags' || bulkAction === 'remove_tags' || bulkAction === 'set_category') {
      return bulkPayloadValue.trim().length > 0
    }
    if (bulkAction === 'set_review_status') {
      return bulkReviewStatus.length > 0
    }
    return true
  }, [bulkAction, bulkPayloadValue, bulkReviewStatus])

  const runBulk = useCallback(async (dryRun: boolean, selected: Set<string>) => {
    setBulkLoading(true)
    setBulkError(null)
    setBulkSuccess(null)
    try {
      const payload = buildBulkPayload()
      const res = await bulkAssets({
        asset_ids: Array.from(selected),
        action: bulkAction as BulkAction,
        dry_run: dryRun,
        payload,
      })
      setBulkPreview(res)
      if (!dryRun) {
        setBulkSuccess(`Operazione completata: ${res.affected} successo, ${res.failed} fallimenti.`)
      }
    } catch (err) {
      setBulkError(err instanceof Error ? err.message : 'Errore sconosciuto')
    } finally {
      setBulkLoading(false)
    }
  }, [bulkAction, buildBulkPayload])

  const needsPayloadInput = useMemo(() => {
    return bulkAction === 'add_tags' || bulkAction === 'remove_tags' || bulkAction === 'set_category'
  }, [bulkAction])

  return {
    bulkAction,
    bulkPayloadValue,
    bulkReviewStatus,
    bulkLoading,
    bulkPreview,
    bulkError,
    bulkSuccess,
    showBulkModal,
    needsPayloadInput,
    openBulkModal,
    closeBulkModal,
    setBulkAction,
    setBulkPayloadValue,
    setBulkReviewStatus,
    canPreview,
    runBulk,
  }
}
