const API_BASE = '/api'

export interface AssetSummary {
  id: string
  source: string
  name: string
  filename: string
  media_type: string
  category: string
  lifecycle_state: string
  created_at: string
  updated_at: string
}

export interface AssetListResponse {
  assets: AssetSummary[]
  count: number
  next_cursor: string
  has_more: boolean
}

export interface AssetLocation {
  kind: string
  uri: string
  external_id?: string
  is_primary?: boolean
  mime_type?: string
  file_size_bytes?: number
  file_hash?: string
}

export interface AssetProcessing {
  step: string
  status: string
  error?: string
  started_at?: string
  completed_at?: string
  attempt_count?: number
}

export interface AssetVersion {
  version_number: number
  source_uri?: string
  file_hash?: string
  file_size?: number
  mime_type?: string
  created_at?: string
}

export interface AssetDetails {
  id: string
  source: string
  name: string
  filename: string
  media_type: string
  category: string
  group?: string
  source_url?: string
  clip_page_url?: string
  thumbnail_url?: string
  duration?: string
  duration_secs?: number
  tags?: string[]
  search_terms?: string[]
  search_text?: string
  lifecycle_state?: string
  metadata?: Record<string, unknown>
  created_at?: string
  updated_at?: string
  license_basis?: string
  review_status?: string
  locations?: AssetLocation[]
  processing?: AssetProcessing[]
  versions?: AssetVersion[]
  embedding_info?: {
    present: boolean
    dimensions: number
    version: string
  }
  [key: string]: unknown
}

export interface AssetFilter {
  source?: string
  media_type?: string
  lifecycle_state?: string
  category?: string
  search?: string
  cursor?: string
  limit?: number
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const url = `${API_BASE}${path}`
  const response = await fetch(url, {
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json',
      ...options.headers,
    },
    ...options,
  })

  if (!response.ok) {
    const text = await response.text().catch(() => 'Unknown error')
    throw new Error(`HTTP ${response.status}: ${text}`)
  }

  return response.json() as Promise<T>
}

export interface AuthResponse {
  ok: boolean
}

export function login(token: string): Promise<AuthResponse> {
  return request<AuthResponse>('/admin/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
  })
}

export function logout(): Promise<AuthResponse> {
  return request<AuthResponse>('/admin/auth/logout', {
    method: 'POST',
  })
}

export function checkAuth(): Promise<AuthResponse> {
  return request<AuthResponse>('/admin/auth/me')
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

export function getSummary(): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>('/assets/operator/summary')
}

export interface OutboxStatusResponse {
  ok: boolean
  counts: Record<string, number>
}

export interface OutboxEventsResponse {
  ok: boolean
  events: any[]
  count: number
}

export function getOutboxStatus(): Promise<OutboxStatusResponse> {
  return request<OutboxStatusResponse>('/assets/operator/outbox/status')
}

export function getOutboxEvents(status?: string): Promise<OutboxEventsResponse> {
  const query = status ? `?status=${encodeURIComponent(status)}` : ''
  return request<OutboxEventsResponse>(`/assets/operator/outbox/events${query}`)
}

export function getOperationsErrors(): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>('/assets/operator/operations/errors')
}

export function getHealth(deep = false): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(`/health${deep ? '?deep=true' : ''}`)
}

export function getReady(): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>('/ready')
}

export function getModels(): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>('/models')
}

export function getQdrantReady(): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>('/qdrant/ready')
}

export function getMediaIndexHealth(): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>('/media/index-health')
}
