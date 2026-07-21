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

export interface AssetPatchRequest {
  name?: string
  category?: string
  group?: string
  tags?: string[]
  search_terms?: string[]
  search_text?: string
  review_status?: string
  description?: string
  language?: string
}

export function patchAsset(id: string, payload: AssetPatchRequest): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(`/assets/operator/assets/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
}

export interface AssetActionsResponse {
  source: string
  canonical_source: string
  is_clip_source: boolean
  reindex?: string
  verify?: string
  reprocess?: string
  reupload?: string
  fix_hash?: string
  reconcile?: string
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

// ── Bulk operations ────────────────────────────────────────────────

export type BulkAction = 'add_tags' | 'remove_tags' | 'set_category' | 'set_review_status' | 'reindex' | 'verify' | 'archive'

export interface BulkOperationRequest {
  asset_ids: string[]
  action: BulkAction
  dry_run: boolean
  payload: Record<string, unknown>
}

export interface BulkChange {
  asset_id: string
  status: 'success' | 'error'
  message?: string
  before?: Record<string, unknown>
  after?: Record<string, unknown>
}

export interface BulkOperationResponse {
  ok: boolean
  action: BulkAction
  dry_run: boolean
  affected: number
  failed: number
  failed_ids: string[]
  changes: BulkChange[]
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

// ── Jobs ─────────────────────────────────────────────────────────────

export interface JobSummary {
  id: string
  type: string
  status: string
  progress: number
  correlation_id?: string
  created_at?: string
  updated_at?: string
  error?: string
}

export interface JobEvent {
  id?: string
  type: string
  message?: string
  data?: Record<string, unknown>
  created_at?: string
}

export interface JobFull {
  id: string
  type: string
  status: string
  correlation_id?: string
  current_stage?: string
  current_step?: string
  progress: number
  error?: string
  result?: Record<string, unknown>
  created_at?: string
  started_at?: string
  updated_at?: string
  timeline?: JobEvent[]
  events?: JobEvent[]
  retryable?: boolean
  job?: Record<string, unknown>
}

export interface JobsListResponse {
  jobs: JobSummary[]
  count: number
}

export function listJobs(filters: { status?: string; type?: string; correlation_id?: string; limit?: number; offset?: number } = {}): Promise<JobsListResponse> {
  const params = new URLSearchParams()
  if (filters.status) params.set('status', filters.status)
  if (filters.type) params.set('type', filters.type)
  if (filters.correlation_id) params.set('correlation_id', filters.correlation_id)
  if (filters.limit !== undefined) params.set('limit', String(filters.limit))
  if (filters.offset !== undefined) params.set('offset', String(filters.offset))
  const query = params.toString()
  return request<JobsListResponse>(`/jobs${query ? `?${query}` : ''}`)
}

export function getJobFull(id: string): Promise<JobFull> {
  return request<JobFull>(`/jobs/${encodeURIComponent(id)}/full`)
}

export function getJobEvents(id: string): Promise<{ events: JobEvent[]; count: number }> {
  return request<{ events: JobEvent[]; count: number }>(`/jobs/${encodeURIComponent(id)}/events`)
}

export function cancelJob(id: string): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(`/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST' })
}

export function retryJob(id: string): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(`/jobs/${encodeURIComponent(id)}/retry`, { method: 'POST' })
}

// ── Script generation jobs ──────────────────────────────────────────

export interface ScriptScene {
  id: string
  index: number
  text: Record<string, string>
  clip?: Record<string, unknown>
  voiceover?: Record<string, { id: string; url?: string; duration?: number }>
}

export interface ScriptDocument {
  id: string
  link: string
}

export interface ScriptRenderJob {
  job_id: string
  status: string
}

export interface ScriptJobFull {
  ok: boolean
  job_id: string
  job?: { id: string; type: string; status: string }
  status: string
  error?: string
  result?: Record<string, unknown>
  current_stage: string
  stages: Record<string, string>
  scenes?: ScriptScene[]
  documents?: Record<string, ScriptDocument>
  render_job?: ScriptRenderJob
  word_count?: number
  error_code?: string
  error_message?: string
  failed_stage?: string
  attempt_count?: number
  next_retry_at?: string
}

export function getScriptJobFull(id: string): Promise<ScriptJobFull> {
  return request<ScriptJobFull>(`/script/jobs/${encodeURIComponent(id)}/full`)
}

// ── Admin Console (schema-driven entity registry) ─────────────────────

export interface AdminFieldDescriptor {
  key: string
  label: string
  type: string
  editable?: boolean
  required?: boolean
  filterable?: boolean
  sortable?: boolean
  options?: string[]
  description?: string
}

export interface AdminActionDescriptor {
  key: string
  label: string
  description?: string
  dangerous?: boolean
}

export interface AdminEntitySchema {
  entity: string
  label: string
  primary_key: string
  readable: boolean
  editable: boolean
  bulk_editable: boolean
  fields: AdminFieldDescriptor[]
  actions: AdminActionDescriptor[]
}

export interface AdminListResponse {
  items: Record<string, unknown>[]
  total: number
  limit: number
  offset: number
}

export function listAdminEntities(): Promise<AdminEntitySchema[]> {
  return request<AdminEntitySchema[]>('/admin/entities')
}

export function getAdminEntitySchema(entity: string): Promise<AdminEntitySchema> {
  return request<AdminEntitySchema>(`/admin/entities/${encodeURIComponent(entity)}/schema`)
}

export function listAdminEntityRecords(entity: string, params: Record<string, string> = {}): Promise<AdminListResponse> {
  const searchParams = new URLSearchParams(params)
  const query = searchParams.toString()
  return request<AdminListResponse>(`/admin/entities/${encodeURIComponent(entity)}${query ? `?${query}` : ''}`)
}

export function getAdminEntityRecord(entity: string, id: string): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(`/admin/entities/${encodeURIComponent(entity)}/${encodeURIComponent(id)}`)
}

export function patchAdminEntityRecord(
  entity: string,
  id: string,
  changes: Record<string, unknown>,
  expectedVersion = 0
): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(`/admin/entities/${encodeURIComponent(entity)}/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ expected_version: expectedVersion, changes }),
  })
}

export function runAdminEntityAction(
  entity: string,
  id: string,
  action: string,
  payload: Record<string, unknown> = {}
): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(
    `/admin/entities/${encodeURIComponent(entity)}/${encodeURIComponent(id)}/actions/${encodeURIComponent(action)}`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    }
  )
}
