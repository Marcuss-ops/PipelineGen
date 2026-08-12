import type { CSSProperties } from 'react'
import type { AdminEntitySchema, AdminFieldDescriptor } from '../api/operations'

export type EntityMeta = Pick<AdminEntitySchema, 'entity' | 'label'>

export interface RecordMap {
  [key: string]: unknown
}

export function getRecordValue(row: RecordMap, key: string): unknown {
  return row[key]
}

export function renderRecordValue(value: unknown): string {
  if (value === null || value === undefined) return ''
  if (Array.isArray(value)) return value.join(', ')
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

const controlStyle: CSSProperties = {
  padding: '0.5rem',
  background: '#1e293b',
  color: '#e2e8f0',
  border: '1px solid #334155',
  borderRadius: '6px',
}

const buttonStyle: CSSProperties = {
  padding: '0.5rem 0.75rem',
  background: '#0f172a',
  color: '#e2e8f0',
  border: '1px solid #334155',
  borderRadius: '6px',
  cursor: 'pointer',
}

export function DatabaseToolbar({
  entities,
  selectedEntity,
  search,
  onEntityChange,
  onSearchChange,
  onExportJSON,
  onExportCSV,
}: {
  entities: EntityMeta[]
  selectedEntity: string
  search: string
  onEntityChange: (entity: string) => void
  onSearchChange: (search: string) => void
  onExportJSON: () => void
  onExportCSV: () => void
}) {
  return (
    <div style={{ display: 'flex', gap: '1rem', marginBottom: '1rem', flexWrap: 'wrap' }}>
      <select value={selectedEntity} onChange={(e) => onEntityChange(e.target.value)} style={controlStyle}>
        {entities.map((e) => (
          <option key={e.entity} value={e.entity}>
            {e.label}
          </option>
        ))}
      </select>
      <input
        type="text"
        placeholder="Search records..."
        value={search}
        onChange={(e) => onSearchChange(e.target.value)}
        style={{ ...controlStyle, minWidth: '200px' }}
      />
      <button onClick={onExportJSON} style={buttonStyle}>Export JSON</button>
      <button onClick={onExportCSV} style={buttonStyle}>Export CSV</button>
    </div>
  )
}

export function DatabaseSchemaPanel({
  schema,
  hiddenColumns,
  onToggleColumn,
}: {
  schema: AdminEntitySchema
  hiddenColumns: Set<string>
  onToggleColumn: (key: string) => void
}) {
  return (
    <div
      style={{
        marginBottom: '1rem',
        padding: '0.75rem',
        background: '#1e293b',
        border: '1px solid #334155',
        borderRadius: '6px',
      }}
    >
      <strong style={{ color: '#38bdf8' }}>{schema.label}</strong>
      <span style={{ marginLeft: '0.5rem', color: '#94a3b8' }}>({schema.fields.length} fields)</span>
      <div style={{ marginTop: '0.5rem', fontSize: '0.85rem', color: '#94a3b8' }}>
        Columns:{' '}
        {schema.fields.map((field) => (
          <label
            key={field.key}
            style={{
              marginRight: '0.75rem',
              cursor: 'pointer',
              color: hiddenColumns.has(field.key) ? '#64748b' : '#e2e8f0',
            }}
          >
            <input
              type="checkbox"
              checked={!hiddenColumns.has(field.key)}
              onChange={() => onToggleColumn(field.key)}
              style={{ marginRight: '0.25rem' }}
            />
            {field.label}
          </label>
        ))}
      </div>
    </div>
  )
}

export function DatabaseTable({
  schema,
  visibleFields,
  records,
  loading,
  sortKey,
  sortDir,
  onSort,
  onAction,
  actionLoading,
}: {
  schema: AdminEntitySchema | null
  visibleFields: AdminFieldDescriptor[]
  records: RecordMap[]
  loading: boolean
  sortKey: string
  sortDir: 'asc' | 'desc'
  onSort: (key: string) => void
  onAction: (id: string, actionKey: string) => void
  actionLoading: string | null
}) {
  return (
    <div style={{ overflowX: 'auto', background: '#1e293b', border: '1px solid #334155', borderRadius: '6px' }}>
      <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
        <thead>
          <tr style={{ background: '#0f172a' }}>
            {visibleFields.map((field) => (
              <th
                key={field.key}
                onClick={() => onSort(field.key)}
                style={{
                  padding: '0.75rem',
                  textAlign: 'left',
                  borderBottom: '1px solid #334155',
                  cursor: 'pointer',
                  whiteSpace: 'nowrap',
                  color: sortKey === field.key ? '#38bdf8' : '#94a3b8',
                }}
              >
                {field.label} {sortKey === field.key ? (sortDir === 'asc' ? '▲' : '▼') : ''}
              </th>
            ))}
            {schema && schema.actions.length > 0 && (
              <th style={{ padding: '0.75rem', textAlign: 'left', borderBottom: '1px solid #334155', color: '#94a3b8' }}>
                Actions
              </th>
            )}
          </tr>
        </thead>
        <tbody>
          {records.map((row, idx) => (
            <tr key={idx} style={{ borderBottom: '1px solid #334155' }}>
              {visibleFields.map((field) => (
                <td key={field.key} style={{ padding: '0.6rem 0.75rem', color: '#e2e8f0' }}>
                  {renderRecordValue(getRecordValue(row, field.key))}
                </td>
              ))}
              {schema && schema.actions.length > 0 && (
                <td style={{ padding: '0.6rem 0.75rem' }}>
                  <div style={{ display: 'flex', gap: '0.5rem' }}>
                    {schema.actions.map((action) => {
                      const id = String(row[schema.primary_key] ?? idx)
                      return (
                        <button
                          key={action.key}
                          onClick={() => onAction(id, action.key)}
                          disabled={actionLoading === `${id}:${action.key}`}
                          style={{
                            padding: '0.25rem 0.5rem',
                            fontSize: '0.75rem',
                            background: action.dangerous ? '#7f1d1d' : '#0f172a',
                            color: '#e2e8f0',
                            border: '1px solid #334155',
                            borderRadius: '4px',
                            cursor: 'pointer',
                          }}
                        >
                          {action.label}
                        </button>
                      )
                    })}
                  </div>
                </td>
              )}
            </tr>
          ))}
          {records.length === 0 && !loading && (
            <tr>
              <td
                colSpan={visibleFields.length + (schema && schema.actions.length > 0 ? 1 : 0)}
                style={{ padding: '1rem', textAlign: 'center', color: '#94a3b8' }}
              >
                No records found.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  )
}

export function DatabasePagination({
  loading,
  recordCount,
  total,
  offset,
  onPageChange,
}: {
  loading: boolean
  recordCount: number
  total: number
  offset: number
  onPageChange: (offset: number) => void
}) {
  const previousDisabled = offset === 0 || loading
  const nextDisabled = offset + 25 >= total || loading
  return (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: '1rem' }}>
      <div style={{ fontSize: '0.85rem', color: '#94a3b8' }}>
        {loading ? 'Loading...' : `${recordCount} of ${total} records`}
      </div>
      <div style={{ display: 'flex', gap: '0.5rem' }}>
        <button
          onClick={() => onPageChange(Math.max(0, offset - 25))}
          disabled={previousDisabled}
          style={{ ...buttonStyle, padding: '0.4rem 0.75rem', opacity: previousDisabled ? 0.5 : 1 }}
        >
          Previous
        </button>
        <button
          onClick={() => onPageChange(offset + 25)}
          disabled={nextDisabled}
          style={{ ...buttonStyle, padding: '0.4rem 0.75rem', opacity: nextDisabled ? 0.5 : 1 }}
        >
          Next
        </button>
      </div>
    </div>
  )
}
