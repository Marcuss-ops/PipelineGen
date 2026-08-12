import { useEffect, useMemo, useState } from 'react'
import {
  listAdminEntities,
  getAdminEntitySchema,
  listAdminEntityRecords,
  runAdminEntityAction,
  AdminEntitySchema,
  AdminFieldDescriptor,
} from '../api/operations'
import {
  DatabasePagination,
  DatabaseSchemaPanel,
  DatabaseTable,
  DatabaseToolbar,
  EntityMeta,
  getRecordValue,
  RecordMap,
} from './DatabaseParts'

const DEFAULT_LIMIT = 25

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

  useEffect(() => {
    listAdminEntities()
      .then((data) => {
        setEntities(data || [])
        if (data && data.length > 0 && !selectedEntity) setSelectedEntity(data[0].entity)
      })
      .catch((err) => setError(err.message || 'Failed to load entities'))
  }, [])

  useEffect(() => {
    if (!selectedEntity) return
    setLoading(true)
    setError(null)
    setSchema(null)
    setRecords([])
    setOffset(0)

    getAdminEntitySchema(selectedEntity)
      .then((nextSchema) => {
        setSchema(nextSchema)
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
    return schema.fields.filter((field) => !hiddenColumns.has(field.key))
  }, [schema, hiddenColumns])

  const handleSort = (key: string) => {
    if (sortKey === key) setSortDir((direction) => (direction === 'asc' ? 'desc' : 'asc'))
    else {
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
    setHiddenColumns((previous) => {
      const next = new Set(previous)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  const filteredRecords = useMemo(() => {
    let rows = [...records]
    if (search.trim()) {
      const term = search.toLowerCase()
      rows = rows.filter((row) => Object.values(row).some((value) => String(value).toLowerCase().includes(term)))
    }
    if (sortKey) {
      rows.sort((a, b) => {
        const av = getRecordValue(a, sortKey)
        const bv = getRecordValue(b, sortKey)
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
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `${selectedEntity}-export.json`
    anchor.click()
    URL.revokeObjectURL(url)
  }

  const exportCSV = () => {
    const headers = visibleFields.map((field) => field.key)
    const csv = [
      headers.join(','),
      ...records.map((row) =>
        headers
          .map((header) => {
            const value = getRecordValue(row, header)
            const stringValue = value == null ? '' : String(value).replace(/"/g, '""')
            return `"${stringValue}"`
          })
          .join(',')
      ),
    ].join('\n')
    const blob = new Blob([csv], { type: 'text/csv' })
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `${selectedEntity}-export.csv`
    anchor.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div style={{ padding: '1.5rem', color: '#e2e8f0' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '1rem' }}>
        <h2 style={{ margin: 0 }}>Database Explorer</h2>
        {error && <span style={{ color: '#f87171', fontSize: '0.9rem' }}>{error}</span>}
      </div>
      <DatabaseToolbar
        entities={entities}
        selectedEntity={selectedEntity}
        search={search}
        onEntityChange={setSelectedEntity}
        onSearchChange={setSearch}
        onExportJSON={exportJSON}
        onExportCSV={exportCSV}
      />
      {schema && <DatabaseSchemaPanel schema={schema} hiddenColumns={hiddenColumns} onToggleColumn={toggleColumn} />}
      <DatabaseTable
        schema={schema}
        visibleFields={visibleFields}
        records={filteredRecords}
        loading={loading}
        sortKey={sortKey}
        sortDir={sortDir}
        onSort={handleSort}
        onAction={handleAction}
        actionLoading={actionLoading}
      />
      <DatabasePagination
        loading={loading}
        recordCount={filteredRecords.length}
        total={total}
        offset={offset}
        onPageChange={handlePageChange}
      />
    </div>
  )
}
