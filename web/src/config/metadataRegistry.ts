/**
 * Metadata Field Registry
 *
 * Single source of truth for how asset fields and metadata keys are
 * presented in the admin dashboard. Every field declares its human
 * label, category, value type, format, unit and visibility rules.
 *
 * The renderer uses this registry to group unknown keys under
 * "Altri metadata" automatically, so future metadata additions are
 * never lost.
 */

export type MetadataValueType =
  | 'string'
  | 'number'
  | 'boolean'
  | 'date'
  | 'duration'
  | 'url'
  | 'email'
  | 'tags'
  | 'json'
  | 'unknown'

export type MetadataCategory =
  | 'identita'
  | 'origine'
  | 'media'
  | 'contenuto'
  | 'ai'
  | 'storage'
  | 'indicizzazione'
  | 'diritti'
  | 'audit'
  | 'altri'

export interface MetadataFieldDefinition {
  /** Canonical key as it appears in the asset or metadata object. */
  key: string
  /** Human-readable Italian label. */
  label: string
  /** Presentation category. */
  category: MetadataCategory
  /** Value type used by the renderer. */
  type: MetadataValueType
  /** Optional display format hint (e.g. "0.00", "YYYY-MM-DD"). */
  format?: string
  /** Optional unit suffix. */
  unit?: string
  /** Whether the field should be shown by default. */
  visible: boolean
  /** Whether the value is sensitive and should be masked by default. */
  sensitive?: boolean
  /** Order inside the category (lower first). */
  order: number
  /** If true, the value is rendered as a clickable link. */
  link?: boolean
}

export interface CategoryDefinition {
  id: MetadataCategory
  label: string
  description?: string
  order: number
}

export const CATEGORIES: CategoryDefinition[] = [
  { id: 'identita', label: 'Identità', order: 10 },
  { id: 'origine', label: 'Origine', order: 20 },
  { id: 'media', label: 'Informazioni media', order: 30 },
  { id: 'contenuto', label: 'Contenuto', order: 40 },
  { id: 'ai', label: 'AI e analisi semantica', order: 50 },
  { id: 'storage', label: 'Storage', order: 60 },
  { id: 'indicizzazione', label: 'Indicizzazione', order: 70 },
  { id: 'diritti', label: 'Diritti', order: 80 },
  { id: 'audit', label: 'Audit', order: 90 },
  { id: 'altri', label: 'Altri metadata', order: 100 },
]

/**
 * Registry entries. Keys that appear at the top level of an asset
 * response (e.g. id, name) and keys nested inside the `metadata`
 * object share the same registry; the renderer resolves them with
 * `getFieldDefinition()`.
 */
export const METADATA_REGISTRY: MetadataFieldDefinition[] = [
  // ── Identità ─────────────────────────────────────────────────────
  { key: 'id', label: 'ID', category: 'identita', type: 'string', visible: true, order: 1 },
  { key: 'name', label: 'Nome', category: 'identita', type: 'string', visible: true, order: 2 },
  { key: 'filename', label: 'Filename', category: 'identita', type: 'string', visible: true, order: 3 },
  { key: 'media_type', label: 'Tipo di media', category: 'identita', type: 'string', visible: true, order: 4 },
  { key: 'source', label: 'Sorgente', category: 'identita', type: 'string', visible: true, order: 5 },
  { key: 'category', label: 'Categoria', category: 'identita', type: 'string', visible: true, order: 6 },
  { key: 'group', label: 'Gruppo', category: 'identita', type: 'string', visible: true, order: 7 },

  // ── Origine ───────────────────────────────────────────────────────
  { key: 'provider', label: 'Provider', category: 'origine', type: 'string', visible: true, order: 1 },
  { key: 'source_url', label: 'Source URL', category: 'origine', type: 'url', link: true, visible: true, order: 2 },
  { key: 'clip_page_url', label: 'Clip page URL', category: 'origine', type: 'url', link: true, visible: true, order: 3 },
  { key: 'youtube_video_id', label: 'YouTube video ID', category: 'origine', type: 'string', visible: true, order: 4 },
  { key: 'channel', label: 'Canale', category: 'origine', type: 'string', visible: true, order: 5 },
  { key: 'search_used', label: 'Ricerca utilizzata', category: 'origine', type: 'string', visible: true, order: 6 },
  { key: 'acquisition_date', label: 'Data di acquisizione', category: 'origine', type: 'date', visible: true, order: 7 },

  // ── Media ──────────────────────────────────────────────────────────
  { key: 'duration', label: 'Durata', category: 'media', type: 'duration', visible: true, order: 1 },
  { key: 'duration_ms', label: 'Durata (ms)', category: 'media', type: 'number', unit: 'ms', visible: true, order: 2 },
  { key: 'duration_secs', label: 'Durata (s)', category: 'media', type: 'number', unit: 's', visible: true, order: 3 },
  { key: 'width', label: 'Larghezza', category: 'media', type: 'number', unit: 'px', visible: true, order: 4 },
  { key: 'height', label: 'Altezza', category: 'media', type: 'number', unit: 'px', visible: true, order: 5 },
  { key: 'fps', label: 'FPS', category: 'media', type: 'number', visible: true, order: 6 },
  { key: 'codec', label: 'Codec', category: 'media', type: 'string', visible: true, order: 7 },
  { key: 'mime_type', label: 'MIME type', category: 'media', type: 'string', visible: true, order: 8 },
  { key: 'file_size', label: 'Dimensione file', category: 'media', type: 'number', visible: true, order: 9 },
  { key: 'file_size_bytes', label: 'Dimensione (byte)', category: 'media', type: 'number', unit: 'B', visible: true, order: 10 },
  { key: 'format', label: 'Formato', category: 'media', type: 'string', visible: true, order: 11 },
  { key: 'orientation', label: 'Orientamento', category: 'media', type: 'string', visible: true, order: 12 },

  // ── Contenuto ─────────────────────────────────────────────────────
  { key: 'transcript', label: 'Transcript', category: 'contenuto', type: 'string', visible: true, order: 1 },
  { key: 'description', label: 'Descrizione', category: 'contenuto', type: 'string', visible: true, order: 2 },
  { key: 'summary', label: 'Summary', category: 'contenuto', type: 'string', visible: true, order: 3 },
  { key: 'language', label: 'Lingua', category: 'contenuto', type: 'string', visible: true, order: 4 },
  { key: 'tags', label: 'Tag', category: 'contenuto', type: 'tags', visible: true, order: 5 },
  { key: 'search_terms', label: 'Search terms', category: 'contenuto', type: 'tags', visible: true, order: 6 },
  { key: 'search_text', label: 'Search text', category: 'contenuto', type: 'string', visible: true, order: 7 },
  { key: 'scenes', label: 'Scene', category: 'contenuto', type: 'json', visible: true, order: 8 },
  { key: 'detected_entities', label: 'Persone o entità rilevate', category: 'contenuto', type: 'tags', visible: true, order: 9 },

  // ── AI e analisi semantica ────────────────────────────────────────
  { key: 'model_used', label: 'Modello utilizzato', category: 'ai', type: 'string', visible: true, order: 1 },
  { key: 'visual_description', label: 'Visual description', category: 'ai', type: 'string', visible: true, order: 2 },
  { key: 'caption', label: 'Caption', category: 'ai', type: 'string', visible: true, order: 3 },
  { key: 'semantic_tags', label: 'Semantic tags', category: 'ai', type: 'tags', visible: true, order: 4 },
  { key: 'quality_score', label: 'Quality score', category: 'ai', type: 'number', visible: true, order: 5 },
  { key: 'embedding_present', label: 'Embedding presente', category: 'ai', type: 'boolean', visible: true, order: 5 },
  { key: 'embedding_model', label: 'Embedding model', category: 'ai', type: 'string', visible: true, order: 6 },
  { key: 'embedding_version', label: 'Embedding version', category: 'ai', type: 'string', visible: true, order: 7 },
  { key: 'embedding_dimensions', label: 'Dimensione embedding', category: 'ai', type: 'number', visible: true, order: 8 },
  { key: 'classifications', label: 'Classificazioni', category: 'ai', type: 'json', visible: true, order: 9 },
  { key: 'confidence_score', label: 'Confidence score', category: 'ai', type: 'number', visible: true, order: 10 },

  // ── Storage ───────────────────────────────────────────────────────
  { key: 'local_path', label: 'Local path', category: 'storage', type: 'string', visible: true, order: 1 },
  { key: 'drive_file_id', label: 'Drive file ID', category: 'storage', type: 'string', visible: true, order: 2 },
  { key: 'drive_link', label: 'Google Drive link', category: 'storage', type: 'url', link: true, visible: true, order: 3 },
  { key: 'download_link', label: 'Download link', category: 'storage', type: 'url', link: true, visible: true, order: 4 },
  { key: 'folder_id', label: 'Folder ID', category: 'storage', type: 'string', visible: true, order: 5 },
  { key: 'folder_path', label: 'Folder path', category: 'storage', type: 'string', visible: true, order: 6 },
  { key: 'file_hash', label: 'File hash', category: 'storage', type: 'string', visible: true, order: 7 },
  { key: 'upload_status', label: 'Stato upload', category: 'storage', type: 'string', visible: true, order: 8 },

  // ── Indicizzazione ────────────────────────────────────────────────
  { key: 'index_state', label: 'Index state', category: 'indicizzazione', type: 'string', visible: true, order: 1 },
  { key: 'enrich_state', label: 'Enrich state', category: 'indicizzazione', type: 'string', visible: true, order: 2 },
  { key: 'lifecycle_state', label: 'Lifecycle state', category: 'indicizzazione', type: 'string', visible: true, order: 3 },
  { key: 'qdrant_collection', label: 'Collezione Qdrant', category: 'indicizzazione', type: 'string', visible: true, order: 4 },
  { key: 'collection_version', label: 'Versione collezione', category: 'indicizzazione', type: 'string', visible: true, order: 5 },
  { key: 'indexed_at', label: 'Indexed at', category: 'indicizzazione', type: 'date', visible: true, order: 6 },
  { key: 'last_error', label: 'Ultimo errore', category: 'indicizzazione', type: 'string', visible: true, order: 7 },
  { key: 'attempts', label: 'Tentativi', category: 'indicizzazione', type: 'number', visible: true, order: 8 },

  // ── Diritti ─────────────────────────────────────────────────────────
  { key: 'rights_status', label: 'Rights status', category: 'diritti', type: 'string', visible: true, order: 1 },
  { key: 'review_status', label: 'Review status', category: 'diritti', type: 'string', visible: true, order: 2 },
  { key: 'license_basis', label: 'License basis', category: 'diritti', type: 'string', visible: true, order: 3 },
  { key: 'owner_channel', label: 'Owner channel', category: 'diritti', type: 'string', visible: true, order: 4 },
  { key: 'allowed_channels', label: 'Canali permessi', category: 'diritti', type: 'tags', visible: true, order: 5 },
  { key: 'allowed_regions', label: 'Regioni permesse', category: 'diritti', type: 'tags', visible: true, order: 6 },
  { key: 'expiration', label: 'Scadenza', category: 'diritti', type: 'date', visible: true, order: 7 },

  // ── Audit ──────────────────────────────────────────────────────────
  { key: 'created_at', label: 'Created at', category: 'audit', type: 'date', visible: true, order: 1 },
  { key: 'updated_at', label: 'Updated at', category: 'audit', type: 'date', visible: true, order: 2 },
  { key: 'deleted_at', label: 'Deleted at', category: 'audit', type: 'date', visible: true, order: 3 },
  { key: 'record_version', label: 'Versione record', category: 'audit', type: 'number', visible: true, order: 4 },
  { key: 'last_modified', label: 'Ultima modifica', category: 'audit', type: 'date', visible: true, order: 5 },
  { key: 'produced_events', label: 'Eventi prodotti', category: 'audit', type: 'json', visible: true, order: 6 },
]

const registryByKey = new Map<string, MetadataFieldDefinition>()
METADATA_REGISTRY.forEach((field) => registryByKey.set(field.key, field))

export function getFieldDefinition(key: string): MetadataFieldDefinition | undefined {
  return registryByKey.get(key)
}

export function getFieldsByCategory(category: MetadataCategory): MetadataFieldDefinition[] {
  return METADATA_REGISTRY.filter((f) => f.category === category).sort((a, b) => a.order - b.order)
}

export function getCategoryDefinition(id: MetadataCategory): CategoryDefinition | undefined {
  return CATEGORIES.find((c) => c.id === id)
}

export interface CategorizedField {
  key: string
  value: unknown
  definition?: MetadataFieldDefinition
}

export interface CategorizedMetadata {
  category: MetadataCategory
  label: string
  order: number
  fields: CategorizedField[]
}

/**
 * Flatten an asset object and its nested `metadata` object into a
 * single map of key -> value. Top-level keys take precedence over
 * metadata keys when names collide.
 */
function flattenAsset(asset: Record<string, unknown>): Record<string, unknown> {
  const flat: Record<string, unknown> = {}
  const metadata = typeof asset.metadata === 'object' && asset.metadata !== null ? (asset.metadata as Record<string, unknown>) : {}

  for (const [key, value] of Object.entries(metadata)) {
    flat[key] = value
  }
  for (const [key, value] of Object.entries(asset)) {
    if (key === 'metadata') continue
    flat[key] = value
  }

  // Flatten nested API objects that contain registry keys.
  const embeddingInfo =
    typeof flat.embedding_info === 'object' && flat.embedding_info !== null
      ? (flat.embedding_info as Record<string, unknown>)
      : null
  if (embeddingInfo) {
    if ('present' in embeddingInfo) flat.embedding_present = embeddingInfo.present
    if ('dimensions' in embeddingInfo) flat.embedding_dimensions = embeddingInfo.dimensions
    if ('version' in embeddingInfo) flat.embedding_version = embeddingInfo.version
  }

  return flat
}

/**
 * Categorize all fields of an asset according to the registry.
 * Unknown keys are placed in the "altri" category.
 */
export function categorizeAsset(asset: Record<string, unknown>): CategorizedMetadata[] {
  const flat = flattenAsset(asset)
  const groups = new Map<MetadataCategory, CategorizedField[]>()

  for (const [key, value] of Object.entries(flat)) {
    const definition = getFieldDefinition(key)
    const category = definition?.category ?? 'altri'
    if (!groups.has(category)) {
      groups.set(category, [])
    }
    groups.get(category)!.push({ key, value, definition })
  }

  return CATEGORIES.map((cat) => ({
    category: cat.id,
    label: cat.label,
    order: cat.order,
    fields: groups.get(cat.id) ?? [],
  })).filter((group) => group.fields.length > 0)
}
