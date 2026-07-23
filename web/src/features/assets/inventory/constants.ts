import { BulkAction } from '../../../api/assets'

export const REVIEW_STATUSES = ['pending', 'approved', 'rejected', 'needs_review']

export const BULK_ACTIONS: { key: BulkAction; label: string; needsPayload: boolean }[] = [
  { key: 'add_tags', label: 'Aggiungi tag', needsPayload: true },
  { key: 'remove_tags', label: 'Rimuovi tag', needsPayload: true },
  { key: 'set_category', label: 'Cambia categoria', needsPayload: true },
  { key: 'set_review_status', label: 'Imposta review status', needsPayload: true },
  { key: 'reindex', label: 'Reindicizza', needsPayload: false },
  { key: 'verify', label: 'Verifica', needsPayload: false },
  { key: 'archive', label: 'Archivia', needsPayload: false },
]
