import { API_BASE, request } from './http'
import type {
  AssetDetails,
  AssetFilter,
  AssetListResponse,
  AssetInventoryFacets,
  AssetPatchRequest,
  AssetActionsResponse,
  VerifyIndexResponse,
  ReindexAssetResponse,
  BulkOperationRequest,
  BulkOperationResponse,
} from './assetTypes'

export * from './assetTypes'

export function getAssetFacets(): Promise<AssetInventoryFacets> {
  return request<AssetInventoryFacets>('/assets/operator/facets')
}

export function listAssets(filters: AssetFilter = {}): Promise<AssetListResponse> {
  const params = new URLSearchParams()
  if (filters.source) params.set('source', filters.source)
  if (filters.media_type) params.set('media_type', filters.media_type)
  if (filters.lifecycle_state) params.set('lifecycle_state', filters.lifecycle_state)
  if (filters.category) params.set('category', filters.category)
  if (filters.search) params.set('search', filters.search)
  if (filters.cursor) params.set('cursor', filters.cursor)
  if (filters.limit) params.set('limit', String(filters.limit))

  const query = params.toString()
  return request<AssetListResponse>(`/assets/operator/assets${query ? `?${query}` : ''}`)
}

export function getAsset(id: string): Promise<AssetDetails> {
  return request<AssetDetails>(`/assets/operator/assets/${encodeURIComponent(id)}`)
}

export function getAssetPreviewUrl(id: string): string {
  return `${API_BASE}/assets/operator/assets/${encodeURIComponent(id)}/preview`
}

export function patchAsset(id: string, payload: AssetPatchRequest): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(`/assets/operator/assets/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export function getAssetActions(id: string): Promise<AssetActionsResponse> {
  return request<AssetActionsResponse>(`/assets/operator/assets/${encodeURIComponent(id)}/actions`)
}

export function triggerClipAction(url: string, payload: Record<string, unknown> = {}): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export function verifyAssetIndex(id: string): Promise<VerifyIndexResponse> {
  return request<VerifyIndexResponse>(`/assets/operator/assets/${encodeURIComponent(id)}/verify-index`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
    })
}

export function reindexAsset(id: string): Promise<ReindexAssetResponse> {
  return request<ReindexAssetResponse>(`/assets/operator/assets/${encodeURIComponent(id)}/reindex`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  })
}

export function bulkAssets(req: BulkOperationRequest): Promise<BulkOperationResponse> {
  return request<BulkOperationResponse>('/assets/operator/bulk', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
}

export function getSummary(): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>('/assets/operator/summary')
}
