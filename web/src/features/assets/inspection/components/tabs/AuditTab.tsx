import styles from '../../AssetInspector.module.css'

export function AuditTab() {
  return (
    <div>
      <h3 className={styles.sectionTitle}>Audit log</h3>
      <p className={styles.emptyText}>
        L&apos;audit log amministrativo sarà disponibile dopo l&apos;implementazione della tabella
        admin_mutation_audit.
      </p>
    </div>
  )
}
