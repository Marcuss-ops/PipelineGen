import { FormState, REVIEW_STATUS_OPTIONS } from '../../types'
import styles from '../../AssetInspector.module.css'
import { FormField } from '../FormField'

interface GeneralTabProps {
  form: FormState
  updateForm: (patch: Partial<FormState>) => void
}

export function GeneralTab({ form, updateForm }: GeneralTabProps) {
  return (
    <div className={styles.formGrid}>
      <div className={styles.formRow}>
        <FormField label="Nome" value={form.name} onChange={(v) => updateForm({ name: v })} />
        <FormField label="Categoria" value={form.category} onChange={(v) => updateForm({ category: v })} />
      </div>
      <div className={styles.formRow}>
        <FormField label="Gruppo" value={form.group} onChange={(v) => updateForm({ group: v })} />
        <div className={styles.formField}>
          <label className={styles.formLabel}>Review status</label>
          <select
            value={form.review_status}
            onChange={(e) => updateForm({ review_status: e.target.value })}
            className={styles.formInput}
          >
            {REVIEW_STATUS_OPTIONS.map((opt) => (
              <option key={opt} value={opt}>
                {opt || '(nessuno)'}
              </option>
            ))}
          </select>
        </div>
      </div>
      <FormField label="Lingua" value={form.language} onChange={(v) => updateForm({ language: v })} />
      <FormField label="Tags (separati da virgola)" value={form.tags} onChange={(v) => updateForm({ tags: v })} />
      <FormField
        label="Search terms (separati da virgola)"
        value={form.search_terms}
        onChange={(v) => updateForm({ search_terms: v })}
      />
      <div className={styles.formField}>
        <label className={styles.formLabel}>Search text</label>
        <textarea
          value={form.search_text}
          onChange={(e) => updateForm({ search_text: e.target.value })}
          className={styles.formTextarea}
        />
      </div>
      <FormField label="Descrizione" value={form.description} onChange={(v) => updateForm({ description: v })} />
    </div>
  )
}
