export interface AssetStatusData {
  code: string
  label: string
  severity: string
  description?: string
  terminal: boolean
  retryable: boolean
}

export interface AssetSummary {
  id: string
  name: string
  filename: string
  source: string
  provider: string
  media_type: string

  lifecycle_state: string
  asset_state: string
  index_state: string

  index_health: AssetStatusData

  has_local_file: boolean
  has_drive_file: boolean
  has_embedding: boolean

  content_hash?: string
  indexed_content_hash?: string
  embedding_version?: string
  collection_version?: string

  pending_outbox_events: number
  last_error?: string

  created_at: string
  updated_at: string
}

export interface AssetListResponse {
  items: AssetSummary[]
  total: number
  next_cursor: string
  has_more: boolean
}

export interface FacetGroup {
  code: string
  label: string
  count: number
}

export interface AssetInventoryFacets {
  media_types: FacetGroup[]
  lifecycle_states: FacetGroup[]
  asset_states: FacetGroup[]
  index_states: FacetGroup[]
  sources: FacetGroup[]
  providers: FacetGroup[]
}

export interface AssetLocation {
  id: number
  asset_id: string
  location_kind: 'local' | 'drive' | 'object_storage'
  uri: string
  external_id?: string
  access_url?: string
  download_url?: string
  mime_type?: string
  file_size_bytes?: number
  file_hash?: string
  is_primary?: boolean
  created_at?: string
  updated_at?: string
}

export interface AssetProcessing {
  asset_id: string
  step: string
  status: string
  error_message?: string
  started_at?: string
  completed_at?: string
  attempt_count?: number
  metadata_json?: string
}

export interface AssetVersion {
  version_number: number
  source_uri?: string
  file_hash?: string
  file_size?: number
  mime_type?: string
  created_at?: string
}

export interface OutboxEventProjection {
  event_type: string
  aggregate_id: string
  event_key: string
  status: string
  attempt_count: number
  last_error?: string
  created_at: string
  updated_at: string
}

export interface AssetDetails {
  id: string
  name: string
  filename: string
  source: string
  provider: string
  media_type: string
  category?: string
  group?: string
  source_url?: string
  clip_page_url?: string
  thumbnail_url?: string
  duration?: string
  duration_secs?: number
  tags?: string[]
  search_terms?: string[]
  search_text?: string
  review_status?: string
  license_basis?: string

  lifecycle_state: string
  asset_state: string
  index_state: string
  index_health: AssetStatusData

  has_local_file: boolean
  has_drive_file: boolean
  has_embedding: boolean

  content_hash?: string
  indexed_content_hash?: string
  embedding_version?: string
  collection_version?: string

  pending_outbox_events: number
  last_error?: string

  locations?: AssetLocation[]
  processing?: AssetProcessing[]
  outbox_events?: OutboxEventProjection[]
  versions?: AssetVersion[]
  metadata?: Record<string, unknown>

  created_at: string
  updated_at: string

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

export interface VerifyIndexResponse {
  asset_id: string
  sqlite: {
    index_state: string
    embedding_present: boolean
    content_hash: string
    indexed_content_hash: string
  }
  outbox: {
    pending: number
  }
  qdrant: {
    checked: boolean
    point_present: boolean
    collection: string
    vector_dimensions: number
    payload_lifecycle_state: string
  }
  consistent: boolean
}

export interface ReindexAssetResponse {
  asset_id: string
  queued: boolean
}

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
