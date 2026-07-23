import { useCallback, useEffect, useMemo, useState } from 'react'
import { AssetDetails, AssetPatchRequest, patchAsset } from '../../../../api/client'
import { FormState, initialForm, parseTags } from '../types'

export interface UseAssetEditorResult {
  form: FormState
  dirty: boolean
  changedFields: AssetPatchRequest
  saving: boolean
  updateForm: (patch: Partial<FormState>) => void
  handleSave: () => Promise<void>
}

export function useAssetEditor(
  id: string | undefined,
  asset: AssetDetails | null,
  onSaved?: () => void
): UseAssetEditorResult {
  const [form, setForm] = useState<FormState>(initialForm(asset))
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    setDirty(false)
    setForm(initialForm(asset))
  }, [id])

  useEffect(() => {
    if (!dirty) {
      setForm(initialForm(asset))
    }
  }, [asset, dirty])

  const updateForm = useCallback((patch: Partial<FormState>) => {
    setForm((prev) => ({ ...prev, ...patch }))
    setDirty(true)
  }, [])

  const changedFields = useMemo<AssetPatchRequest>(() => {
    if (!asset) return {}
    const changes: AssetPatchRequest = {}
    if (form.name !== (asset.name ?? '')) changes.name = form.name
    if (form.category !== (asset.category ?? '')) changes.category = form.category
    if (form.group !== (asset.group ?? '')) changes.group = form.group
    if (form.search_text !== (asset.search_text ?? '')) changes.search_text = form.search_text
    if (form.review_status !== (asset.review_status ?? '')) changes.review_status = form.review_status

    const tags = parseTags(form.tags)
    if (JSON.stringify(tags) !== JSON.stringify(asset.tags ?? [])) changes.tags = tags

    const searchTerms = parseTags(form.search_terms)
    if (JSON.stringify(searchTerms) !== JSON.stringify(asset.search_terms ?? [])) changes.search_terms = searchTerms

    const origDesc = String(asset.metadata?.description ?? '')
    if (form.description !== origDesc) changes.description = form.description

    const origLang = String(asset.metadata?.language ?? '')
    if (form.language !== origLang) changes.language = form.language

    return changes
  }, [form, asset])

  const handleSave = useCallback(async () => {
    if (!id || !Object.keys(changedFields).length) return
    setSaving(true)
    try {
      await patchAsset(id, changedFields)
      setDirty(false)
      onSaved?.()
    } finally {
      setSaving(false)
    }
  }, [id, changedFields, onSaved])

  return { form, dirty, changedFields, saving, updateForm, handleSave }
}
