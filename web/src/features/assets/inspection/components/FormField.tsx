import styles from '../AssetInspector.module.css'

interface FormFieldProps {
  label: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
}

export function FormField({ label, value, onChange, placeholder }: FormFieldProps) {
  return (
    <div className={styles.formField}>
      <label className={styles.formLabel}>{label}</label>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className={styles.formInput}
      />
    </div>
  )
}
