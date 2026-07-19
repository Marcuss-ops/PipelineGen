import { useMemo, useState } from 'react'
import {
  categorizeAsset,
  CategorizedField,
  MetadataValueType,
  getCategoryDefinition,
  MetadataCategory,
} from '../config/metadataRegistry'

interface MetadataViewerProps {
  /** Asset object returned by the admin API. */
  asset: Record<string, unknown>
  /** Optional title shown above the viewer. */
  title?: string
  /** If true, all categories start collapsed. */
  defaultCollapsed?: boolean
}

const categoryStyles: Record<string, React.CSSProperties> = {
  identita: { borderLeft: '4px solid #38bdf8' },
  origine: { borderLeft: '4px solid #818cf8' },
  media: { borderLeft: '4px solid #f472b6' },
  contenuto: { borderLeft: '4px solid #34d399' },
  ai: { borderLeft: '4px solid #a78bfa' },
  storage: { borderLeft: '4px solid #fbbf24' },
  indicizzazione: { borderLeft: '4px solid #f87171' },
  diritti: { borderLeft: '4px solid #60a5fa' },
  audit: { borderLeft: '4px solid #94a3b8' },
  altri: { borderLeft: '4px solid #cbd5e1' },
}

function formatValue(value: unknown, type: MetadataValueType | undefined): string {
  if (value === null || value === undefined) return '-'
  if (typeof value === 'string' && value.trim() === '') return '-'

  switch (type) {
    case 'date':
      try {
        const d = new Date(value as string | number)
        return isNaN(d.getTime()) ? String(value) : d.toLocaleString('it-IT')
      } catch {
        return String(value)
      }
    case 'duration':
      if (typeof value === 'number') {
        const secs = value / 1000
        return `${secs.toFixed(2)} s`
      }
      return String(value)
    case 'number':
      return typeof value === 'number' ? value.toLocaleString('it-IT') : String(value)
    case 'boolean':
      return value ? 'Sì' : 'No'
    case 'tags':
      return Array.isArray(value) ? value.join(', ') : String(value)
    case 'json':
      try {
        return JSON.stringify(value, null, 2)
      } catch {
        return String(value)
      }
    default:
      return String(value)
  }
}

function isEmptyValue(value: unknown): boolean {
  if (value === null || value === undefined) return true
  if (typeof value === 'string' && value.trim() === '') return true
  if (Array.isArray(value) && value.length === 0) return true
  if (typeof value === 'object' && value !== null && Object.keys(value).length === 0) return true
  return false
}

function FieldValue({ value, type, link }: { value: unknown; type: MetadataValueType | undefined; link?: boolean }) {
  if (isEmptyValue(value)) {
    return <span style={{ color: '#94a3b8', fontStyle: 'italic' }}>mancante</span>
  }

  if (type === 'url' || (link && typeof value === 'string')) {
    return (
      <a
        href={String(value)}
        target="_blank"
        rel="noopener noreferrer"
        style={{ color: '#38bdf8', textDecoration: 'none', wordBreak: 'break-all' }}
      >
        {String(value)}
      </a>
    )
  }

  if (type === 'tags' && Array.isArray(value)) {
    return (
      <span style={{ display: 'flex', flexWrap: 'wrap', gap: '0.35rem' }}>
        {value.map((tag, idx) => (
          <span
            key={idx}
            style={{
              background: 'rgba(56,189,248,0.1)',
              color: '#38bdf8',
              padding: '0.15rem 0.5rem',
              borderRadius: '9999px',
              fontSize: '0.75rem',
            }}
          >
            {String(tag)}
          </span>
        ))}
      </span>
    )
  }

  if (type === 'json') {
    return (
      <pre
        style={{
          background: '#0f172a',
          color: '#e2e8f0',
          padding: '0.75rem',
          borderRadius: '6px',
          fontSize: '0.75rem',
          overflowX: 'auto',
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-all',
          maxHeight: '200px',
          overflowY: 'auto',
        }}
      >
        {formatValue(value, type)}
      </pre>
    )
  }

  return <span style={{ wordBreak: 'break-word' }}>{formatValue(value, type)}</span>
}

function MetadataField({ field }: { field: CategorizedField }) {
  const label = field.definition?.label ?? field.key
  const type = field.definition?.type

  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: '180px 1fr',
        gap: '0.75rem',
        padding: '0.5rem 0',
        borderBottom: '1px solid #334155',
        alignItems: 'start',
      }}
    >
      <span style={{ color: '#94a3b8', fontSize: '0.85rem', fontWeight: 500 }}>{label}</span>
      <FieldValue value={field.value} type={type} link={field.definition?.link} />
    </div>
  )
}

function CategorySection({
  category,
  fields,
  isOpen,
  onToggle,
}: {
  category: MetadataCategory
  fields: CategorizedField[]
  isOpen: boolean
  onToggle: () => void
}) {
  const catDef = useMemo(() => {
    const def = getCategoryDefinition(category)
    return def ?? { label: category, order: 999 }
  }, [category])

  return (
    <div
      style={{
        background: '#1e293b',
        border: '1px solid #334155',
        borderRadius: '8px',
        marginBottom: '1rem',
        overflow: 'hidden',
        ...categoryStyles[category],
      }}
    >
      <button
        onClick={onToggle}
        style={{
          width: '100%',
          padding: '0.85rem 1rem',
          background: 'transparent',
          border: 'none',
          color: '#e2e8f0',
          fontWeight: 600,
          textAlign: 'left',
          cursor: 'pointer',
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
        }}
      >
        <span>{catDef.label}</span>
        <span style={{ color: '#94a3b8', fontSize: '0.85rem' }}>
          {fields.length} {fields.length === 1 ? 'campo' : 'campi'} {isOpen ? '▾' : '▸'}
        </span>
      </button>
      {isOpen && (
        <div style={{ padding: '0 1rem 1rem' }}>
          {fields.map((field) => (
            <MetadataField key={field.key} field={field} />
          ))}
        </div>
      )}
    </div>
  )
}

function RawJsonTab({ asset }: { asset: Record<string, unknown> }) {
  const [copied, setCopied] = useState(false)
  const raw = useMemo(() => JSON.stringify(asset, null, 2), [asset])

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(raw)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch (err) {
      // eslint-disable-next-line no-console
      console.warn('Failed to copy JSON to clipboard', err)
    }
  }

  const handleDownload = () => {
    const blob = new Blob([raw], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'asset.json'
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div>
      <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '0.75rem' }}>
        <button onClick={handleCopy} style={tabButtonStyle}>
          {copied ? 'Copiato!' : 'Copia JSON'}
        </button>
        <button onClick={handleDownload} style={tabButtonStyle}>
          Scarica JSON
        </button>
      </div>
      <pre
        style={{
          background: '#0f172a',
          color: '#e2e8f0',
          padding: '1rem',
          borderRadius: '8px',
          fontSize: '0.75rem',
          overflowX: 'auto',
          maxHeight: '60vh',
          overflowY: 'auto',
          border: '1px solid #334155',
        }}
      >
        {raw}
      </pre>
    </div>
  )
}

const tabButtonStyle: React.CSSProperties = {
  background: '#1e293b',
  color: '#e2e8f0',
  border: '1px solid #334155',
  borderRadius: '6px',
  padding: '0.4rem 0.75rem',
  cursor: 'pointer',
  fontSize: '0.85rem',
}

export default function MetadataViewer({ asset, title = 'Metadata', defaultCollapsed = false }: MetadataViewerProps) {
  const categorized = useMemo(() => categorizeAsset(asset), [asset])
  const [activeTab, setActiveTab] = useState<'categorized' | 'raw'>('categorized')
  const [openCategories, setOpenCategories] = useState<Record<string, boolean>>(() => {
    const initial: Record<string, boolean> = {}
    categorized.forEach((cat) => {
      initial[cat.category] = !defaultCollapsed
    })
    return initial
  })

  const toggleCategory = (category: string) => {
    setOpenCategories((prev) => ({ ...prev, [category]: !prev[category] }))
  }

  if (!asset || Object.keys(asset).length === 0) {
    return <div style={{ color: '#94a3b8' }}>Nessun metadata disponibile.</div>
  }

  return (
    <div style={{ color: '#e2e8f0', fontFamily: 'system-ui, sans-serif' }}>
      <h3 style={{ marginBottom: '1rem', color: '#38bdf8' }}>{title}</h3>
      <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '1rem', borderBottom: '1px solid #334155' }}>
        <button
          onClick={() => setActiveTab('categorized')}
          style={{
            ...tabButtonStyle,
            borderBottom: activeTab === 'categorized' ? '2px solid #38bdf8' : undefined,
            borderRadius: '6px 6px 0 0',
            background: activeTab === 'categorized' ? '#0f172a' : '#1e293b',
          }}
        >
          Categorie
        </button>
        <button
          onClick={() => setActiveTab('raw')}
          style={{
            ...tabButtonStyle,
            borderBottom: activeTab === 'raw' ? '2px solid #38bdf8' : undefined,
            borderRadius: '6px 6px 0 0',
            background: activeTab === 'raw' ? '#0f172a' : '#1e293b',
          }}
        >
          Raw JSON
        </button>
      </div>
      {activeTab === 'categorized' ? (
        categorized.map((cat) => (
          <CategorySection
            key={cat.category}
            category={cat.category}
            fields={cat.fields}
            isOpen={!!openCategories[cat.category]}
            onToggle={() => toggleCategory(cat.category)}
          />
        ))
      ) : (
        <RawJsonTab asset={asset} />
      )}
    </div>
  )
}
