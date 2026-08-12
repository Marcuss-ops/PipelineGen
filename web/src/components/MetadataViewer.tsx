import { useMemo, useState } from 'react'
import { categorizeAsset, MetadataCategory } from '../config/metadataRegistry'
import { CategorySection, RawJsonTab, tabButtonStyle } from './MetadataParts'

interface MetadataViewerProps {
  /** Asset object returned by the admin API. */
  asset: Record<string, unknown>
  /** Optional title shown above the viewer. */
  title?: string
  /** If true, all categories start collapsed. */
  defaultCollapsed?: boolean
}

export default function MetadataViewer({ asset, title = 'Metadata', defaultCollapsed = false }: MetadataViewerProps) {
  const categorized = useMemo(() => categorizeAsset(asset), [asset])
  const [activeTab, setActiveTab] = useState<'categorized' | 'raw'>('categorized')
  const [openCategories, setOpenCategories] = useState<Record<string, boolean>>(() => {
    const initial: Record<string, boolean> = {}
    categorized.forEach((category) => { initial[category.category] = !defaultCollapsed })
    return initial
  })

  const toggleCategory = (category: string) => {
    setOpenCategories((previous) => ({ ...previous, [category]: !previous[category] }))
  }

  if (!asset || Object.keys(asset).length === 0) return <div style={{ color: '#94a3b8' }}>Nessun metadata disponibile.</div>

  return (
    <div style={{ color: '#e2e8f0', fontFamily: 'system-ui, sans-serif' }}>
      <h3 style={{ marginBottom: '1rem', color: '#38bdf8' }}>{title}</h3>
      <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '1rem', borderBottom: '1px solid #334155' }}>
        <button onClick={() => setActiveTab('categorized')} style={{ ...tabButtonStyle, borderBottom: activeTab === 'categorized' ? '2px solid #38bdf8' : undefined, borderRadius: '6px 6px 0 0', background: activeTab === 'categorized' ? '#0f172a' : '#1e293b' }}>Categorie</button>
        <button onClick={() => setActiveTab('raw')} style={{ ...tabButtonStyle, borderBottom: activeTab === 'raw' ? '2px solid #38bdf8' : undefined, borderRadius: '6px 6px 0 0', background: activeTab === 'raw' ? '#0f172a' : '#1e293b' }}>Raw JSON</button>
      </div>
      {activeTab === 'categorized' ? categorized.map((category) => <CategorySection key={category.category} category={category.category as MetadataCategory} fields={category.fields} isOpen={!!openCategories[category.category]} onToggle={() => toggleCategory(category.category)} />) : <RawJsonTab asset={asset} />}
    </div>
  )
}
