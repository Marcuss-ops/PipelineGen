import { AssetDetails } from '../../../api/assetTypes'

export type TabKey = 'panoramica' | 'pipeline' | 'indicizzazione' | 'storage' | 'eventi'

export interface FormState {
  name: string
  category: string
  group: string
  tags: string
  search_terms: string
  search_text: string
  review_status: string
  description: string
  language: string
}

export const REVIEW_STATUS_OPTIONS = ['', 'none', 'pending', 'approved', 'rejected']

// Lifecycle states where the asset is in flux; polling is paused when not transient.
export const TRANSIENT_LIFECYCLE_STATES = ['PROCESSING', 'PENDING', 'STAGING', 'PREPARING']

export function initialForm(asset: AssetDetails | null): FormState {
  return {
    name: asset?.name ?? '',
    category: asset?.category ?? '',
    group: asset?.group ?? '',
    tags: (asset?.tags ?? []).join(', '),
    search_terms: (asset?.search_terms ?? []).join(', '),
    search_text: asset?.search_text ?? '',
    review_status: asset?.review_status ?? '',
    description: String(asset?.metadata?.description ?? ''),
    language: String(asset?.metadata?.language ?? ''),
  }
}

export function parseTags(value: string): string[] {
  return value
    .split(/[,;]/)
    .map((t) => t.trim())
    .filter((t) => t.length > 0)
}

export function isAssetTransient(asset: AssetDetails | null): boolean {
  if (!asset) return false
  return asset.lifecycle_state ? TRANSIENT_LIFECYCLE_STATES.includes(asset.lifecycle_state) : false
}
