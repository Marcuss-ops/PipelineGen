import { useCallback, useEffect, useState } from 'react'
import {
  AssetActionsResponse,
  AssetDetails,
  getAsset,
  getAssetActions,
  reindexAsset,
  ReindexAssetResponse,
  verifyAssetIndex,
  VerifyIndexResponse,
} from '../../../../api/assets'
import { usePollingQuery } from '../../../../hooks/usePollingQuery'
import { isAssetTransient } from '../types'

export interface UseAssetInspectionResult {
  asset: AssetDetails | null
  loading: boolean
  error: string | null
  refresh: () => void
  actions: AssetActionsResponse | null
  verifyResult: VerifyIndexResponse | null
  verifyLoading: boolean
  reindexLoading: boolean
  handleVerify: () => Promise<VerifyIndexResponse>
  handleReindex: () => Promise<ReindexAssetResponse>
  resetVerifyResult: () => void
}

export function useAssetInspection(id: string | undefined): UseAssetInspectionResult {
  const [actions, setActions] = useState<AssetActionsResponse | null>(null)
  const [verifyResult, setVerifyResult] = useState<VerifyIndexResponse | null>(null)
  const [verifyLoading, setVerifyLoading] = useState(false)
  const [reindexLoading, setReindexLoading] = useState(false)
  const [pausePolling, setPausePolling] = useState(false)

  const {
    data: asset,
    loading,
    error,
    refresh,
  } = usePollingQuery<AssetDetails>({
    queryFn: async () => {
      if (!id) throw new Error('ID mancante')
      return getAsset(id)
    },
    interval: 5000,
    enabled: !!id,
    pause: pausePolling,
  })

  useEffect(() => {
    setPausePolling(!isAssetTransient(asset))
  }, [asset])

  useEffect(() => {
    if (!id || !asset) {
      setActions(null)
      return
    }
    let cancelled = false
    getAssetActions(id)
      .then((acts) => {
        if (!cancelled) setActions(acts)
      })
      .catch(() => {
        if (!cancelled) setActions(null)
      })
    return () => {
      cancelled = true
    }
  }, [id, asset])

  const resetVerifyResult = useCallback(() => {
    setVerifyResult(null)
  }, [])

  const handleVerify = useCallback(async (): Promise<VerifyIndexResponse> => {
    if (!id) throw new Error('ID mancante')
    setVerifyLoading(true)
    try {
      const res = await verifyAssetIndex(id)
      setVerifyResult(res)
      return res
    } finally {
      setVerifyLoading(false)
    }
  }, [id])

  const handleReindex = useCallback(async (): Promise<ReindexAssetResponse> => {
    if (!id) throw new Error('ID mancante')
    setReindexLoading(true)
    try {
      const res = await reindexAsset(id)
      if (res.queued) {
        refresh()
      }
      return res
    } finally {
      setReindexLoading(false)
    }
  }, [id, refresh])

  return {
    asset,
    loading,
    error,
    refresh,
    actions,
    verifyResult,
    verifyLoading,
    reindexLoading,
    handleVerify,
    handleReindex,
    resetVerifyResult,
  }
}
