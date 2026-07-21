import { useEffect, useMemo, useState } from 'react'
import {
  listAdminEntities,
  getAdminEntitySchema,
  listAdminEntityRecords,
  runAdminEntityAction,
  AdminEntitySchema,
  AdminFieldDescriptor,
} from '../api/client'

type EntityMeta = Pick<AdminEntitySchema, 'entity' | 'label'>

interface RecordMap {
  [key: string]: unknown
}

const DEFAULT_LIMIT = 25

function getValue(row: RecordMap, key: string): unknown {
  return row[key]
}

function renderValue(value: unknown): string {
  if (value === null || value === undefined) return ''
  if (Array.isArray(value)) return value.join(', ')
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

export default function Database() {
  const [entities, setEntities] = useState<EntityMeta[]>([])
  const [selectedEntity, setSelectedEntity] = useState<string>('')
  const [schema, setSchema] = useState<AdminEntitySchema | null>(null)
  const [records, setRecords] = useState<RecordMap[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [offset, setOffset] = useState(0)
  const [search, setSearch] = useState('')
  const [sortKey, setSortKey] = useState<string>('')
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('asc')
  const [hiddenColumns, setHiddenColumns] = useState<Set<string>>(new Set())
  const [actionLoading, setActionLoading] = useState<string | null>(null)

  // Load entity list on mount
  useEffect(() => {
    listAdminEntities()
      .then((data) => {
        setEntities(data || [])
        if (data && data.length > 0 && !selectedEntity) {
          setSelectedEntity(data[0].entity)
        }
      })
      .catch((err) => setError(err.message || 'Failed to load entities'))
  }, [])

  // Load schema and records when entity or pagination changes
  useEffect(() => {
    if (!selectedEntity) return
    setLoading(true)
    setError(null)
    setSchema(null)
    setRecords([])
    setOffset(0)

    getAdminEntitySchema(selectedEntity)
      .then((s) => {
        setSchema(s)
        return listAdminEntityRecords(selectedEntity, { limit: String(DEFAULT_LIMIT), offset: '0' })
      })
      .then((res) => {
        setRecords(res.items || [])
        setTotal(res.total || 0)
      })
      .catch((err) => setError(err.message || 'Failed to load records'))
      .finally(() => setLoading(false))
  }, [selectedEntity])

  const visibleFields = useMemo<AdminFieldDescriptor[]>(() => {
    if (!schema) return []
    return schema.fields.filter((f) => !hiddenColumns.has(f.key))
  }, [schema, hiddenColumns])

  const handleSort = (key: string) => {
    if (sortKey === key) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  const handleAction = async (id: string, actionKey: string) => {
    if (!selectedEntity) return
    setActionLoading(`${id}:${actionKey}`)
    try {
      await runAdminEntityAction(selectedEntity, id, actionKey, {})
      const res = await listAdminEntityRecords(selectedEntity, {
        limit: String(DEFAULT_LIMIT),
        offset: String(offset),
      })
      setRecords(res.items || [])
      setTotal(res.total || 0)
    } catch (err: any) {
      setError(err.message || 'Action failed')
    } finally {
      setActionLoading(null)
    }
  }

  const handlePageChange = async (newOffset: number) => {
    if (!selectedEntity) return
    setLoading(true)
    try {
      const res = await listAdminEntityRecords(selectedEntity, {
        limit: String(DEFAULT_LIMIT),
        offset: String(newOffset),
      })
      setRecords(res.items || [])
      setTotal(res.total || 0)
      setOffset(newOffset)
    } catch (err: any) {
      setError(err.message || 'Failed to load page')
    } finally {
      setLoading(false)
    }
  }

  const toggleColumn = (key: string) => {
    setHiddenColumns((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  const filteredRecords = useMemo(() => {
    let rows = [...records]
    if (search.trim()) {
      const term = search.toLowerCase()
      rows = rows.filter((r) =>
        Object.values(r).some((v) => String(v).toLowerCase().includes(term))
      )
    }
    if (sortKey) {
      rows.sort((a, b) => {
        const av = getValue(a, sortKey)
        const bv = getValue(b, sortKey)
        if (av == null) return sortDir === 'asc' ? -1 : 1
        if (bv == null) return sortDir === 'asc' ? 1 : -1
        const as = String(av)
        const bs = String(bv)
        if (as < bs) return sortDir === 'asc' ? -1 : 1
        if (as > bs) return sortDir === 'asc' ? 1 : -1
        return 0
      })
    }
    return rows
  }, [records, search, sortKey, sortDir])

  const exportJSON = () => {
    const blob = new Blob([JSON.stringify(records, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${selectedEntity}-export.json`
    a.click()
    URL.revokeObjectURL(url)
  }

  const exportCSV = () => {
    const headers = visibleFields.map((f) => f.key)
    const csv = [
      headers.join(','),
      ...records.map((row) =>
        headers
          .map((h) => {
            const v = getValue(row, h)
            const str = v == null ? '' : String(v).replace(/"/g, '""')
            return `"${str}"`
          })
          .join(',')
      ),
    ].join('\n')
    const blob = new Blob([csv], { type: 'text/csv' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${selectedEntity}-export.csv`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div style={{ padding: '1.5rem', color: '#e2e8f0' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '1rem' }}>
        <h2 style={{ margin: 0 }}>Database Explorer</h2>
        {error && (
          <span style={{ color: '#f87171', fontSize: '0.9rem' }}>{error}</span>
        )}
      </div>

      <div style={{ display: 'flex', gap: '1rem', marginBottom: '1rem', flexWrap: 'wrap' }}>
        <select
          value={selectedEntity}
          onChange={(e) => setSelectedEntity(e.target.value)}
          style={{
            padding: '0.5rem',
            background: '#1e293b',
            color: '#e2e8f0',
            border: '1px solid #334155',
            borderRadius: '6px',
          }}
        >
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
          onChange={(e) => setSearch(e.target.value)}
          style={{
            padding: '0.5rem',
            background: '#1e293b',
            color: '#e2e8f0',
            border: '1px solid #334155',
            borderRadius: '6px',
            minWidth: '200px',
          }}
        />

        <button
          onClick={exportJSON}
          style={{
            padding: '0.5rem 0.75rem',
            background: '#0f172a',
            color: '#e2e8f0',
            border: '1px solid #334155',
            borderRadius: '6px',
            cursor: 'pointer',
          }}
        >
          Export JSON
        </button>
        <button
          onClick={exportCSV}
          style={{
            padding: '0.5rem 0.75rem',
            background: '#0f172a',
            color: '#e2e8f0',
            border: '1px solid #334155',
            borderRadius: '6px',
            cursor: 'pointer',
          }}
        >
          Export CSV
        </button>
      </div>

      {schema && (
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
            {schema.fields.map((f) => (
              <label
                key={f.key}
                style={{
                  marginRight: '0.75rem',
                  cursor: 'pointer',
                  color: hiddenColumns.has(f.key) ? '#64748b' : '#e2e8f0',
                }}
              >
                <input
                  type="checkbox"
                  checked={!hiddenColumns.has(f.key)}
                  onChange={() => toggleColumn(f.key)}
                  style={{ marginRight: '0.25rem' }}
                />
                {f.label}
              </label>
            ))}
          </div>
        </div>
      )}

      <div
        style={{
          overflowX: 'auto',
          background: '#1e293b',
          border: '1px solid #334155',
          borderRadius: '6px',
        }}
      >
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
          <thead>
            <tr style={{ background: '#0f172a' }}>
              {visibleFields.map((f) => (
                <th
                  key={f.key}
                  onClick={() => handleSort(f.key)}
                  style={{
                    padding: '0.75rem',
                    textAlign: 'left',
                    borderBottom: '1px solid #334155',
                    cursor: 'pointer',
                    whiteSpace: 'nowrap',
                    color: sortKey === f.key ? '#38bdf8' : '#94a3b8',
                  }}
                >
                  {f.label} {sortKey === f.key ? (sortDir === 'asc' ? '▲' : '▼') : ''}
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
            {filteredRecords.map((row, idx) => (
              <tr key={idx} style={{ borderBottom: '1px solid #334155' }}>
                {visibleFields.map((f) => (
                  <td key={f.key} style={{ padding: '0.6rem 0.75rem', color: '#e2e8f0' }}>
                    {renderValue(getValue(row, f.key))}
                  </td>
                ))}
                {schema && schema.actions.length > 0 && (
                  <td style={{ padding: '0.6rem 0.75rem' }}>
                    <div style={{ display: 'flex', gap: '0.5rem' }}>
                      {schema.actions.map((a) => (
                        <button
                          key={a.key}
                          onClick={() => handleAction(String(row[schema.primary_key] ?? idx), a.key)}
                          disabled={actionLoading === `${String(row[schema.primary_key] ?? idx)}:${a.key}`}
                          style={{
                            padding: '0.25rem 0.5rem',
                            fontSize: '0.75rem',
                            background: a.dangerous ? '#7f1d1d' : '#0f172a',
                            color: '#e2e8f0',
                            border: '1px solid #334155',
                            borderRadius: '4px',
                            cursor: 'pointer',
                          }}
                        >
                          {a.label}
                        </button>
                      ))}
                    </div>
                  </td>
                )}
              </tr>
            ))}
            {filteredRecords.length === 0 && !loading && (
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

      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: '1rem' }}>
        <div style={{ fontSize: '0.85rem', color: '#94a3b8' }}>
          {loading ? 'Loading...' : `${filteredRecords.length} of ${total} records`}
        </div>
        <div style={{ display: 'flex', gap: '0.5rem' }}>
          <button
            onClick={() => handlePageChange(Math.max(0, offset - DEFAULT_LIMIT))}
            disabled={offset === 0 || loading}
            style={{
              padding: '0.4rem 0.75rem',
              background: '#0f172a',
              color: '#e2e8f0',
              border: '1px solid #334155',
              borderRadius: '6px',
              cursor: 'pointer',
              opacity: offset === 0 || loading ? 0.5 : 1,
            }}
          >
            Previous
          </button>
          <button
            onClick={() => handlePageChange(offset + DEFAULT_LIMIT)}
            disabled={offset + DEFAULT_LIMIT >= total || loading}
            style={{
              padding: '0.4rem 0.75rem',
              background: '#0f172a',
              color: '#e2e8f0',
              border: '1px solid #334155',
              borderRadius: '6px',
              cursor: 'pointer',
              opacity: offset + DEFAULT_LIMIT >= total || loading ? 0.5 : 1,
            }}
          >
            Next
          </button>
        </div>
      </div>
    </div>
  )
}
