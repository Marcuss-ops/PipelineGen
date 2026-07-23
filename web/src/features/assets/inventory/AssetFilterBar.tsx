import { AssetInventoryFacets, FacetGroup } from '../../../api/assets'
import { FilterState } from './types'
import { inputStyle, secondaryButtonStyle, viewToggleStyle } from './styles'

interface AssetFilterBarProps {
  filters: FilterState
  facets: AssetInventoryFacets
  facetsError: string | null
  activeFilterCount: number
  viewMode: 'table' | 'cards'
  onFilterChange: (key: keyof FilterState, value: string) => void
  onClearFilters: () => void
  onViewModeChange: (mode: 'table' | 'cards') => void
}

function toSelectOptions(groups: FacetGroup[]): { value: string; label: string }[] {
  return [{ value: '', label: 'Tutti' }, ...groups.map((g) => ({ value: g.code, label: `${g.label} (${g.count})` }))]
}

function FilterSelect({
  label,
  value,
  onChange,
  options,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  options: { value: string; label: string }[]
}) {
  return (
    <div style={{ flex: '1 1 160px', minWidth: '140px' }}>
      <label style={{ display: 'block', fontSize: '0.8rem', color: '#94a3b8', marginBottom: '0.35rem' }}>{label}</label>
      <select value={value} onChange={(e) => onChange(e.target.value)} style={inputStyle}>
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </select>
    </div>
  )
}

export function AssetFilterBar({
  filters,
  facets,
  facetsError,
  activeFilterCount,
  viewMode,
  onFilterChange,
  onClearFilters,
  onViewModeChange,
}: AssetFilterBarProps) {
  return (
    <>
      {facetsError && (
        <div
          style={{
            background: 'rgba(251,191,36,0.1)',
            border: '1px solid #fbbf24',
            color: '#fbbf24',
            padding: '0.75rem 1rem',
            borderRadius: '8px',
            marginBottom: '1rem',
            fontSize: '0.85rem',
          }}
        >
          Filtri non caricati: {facetsError}
        </div>
      )}

      <div
        style={{
          background: '#1e293b',
          border: '1px solid #334155',
          borderRadius: '8px',
          padding: '1rem',
          marginBottom: '1.5rem',
        }}
      >
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: '1rem', alignItems: 'end' }}>
          <div style={{ flex: '1 1 240px', minWidth: '200px' }}>
            <label style={{ display: 'block', fontSize: '0.8rem', color: '#94a3b8', marginBottom: '0.35rem' }}>
              Ricerca
            </label>
            <input
              type="text"
              value={filters.search}
              onChange={(e) => onFilterChange('search', e.target.value)}
              placeholder="Cerca per nome, ID, categoria..."
              style={inputStyle}
            />
          </div>

          <FilterSelect
            label="Tipo media"
            value={filters.media_type}
            onChange={(v) => onFilterChange('media_type', v)}
            options={toSelectOptions(facets.media_types)}
          />

          <FilterSelect
            label="Sorgente"
            value={filters.source}
            onChange={(v) => onFilterChange('source', v)}
            options={toSelectOptions(facets.sources)}
          />

          <FilterSelect
            label="Stato lifecycle"
            value={filters.lifecycle_state}
            onChange={(v) => onFilterChange('lifecycle_state', v)}
            options={toSelectOptions(facets.lifecycle_states)}
          />

          <div style={{ flex: '1 1 180px', minWidth: '150px' }}>
            <label style={{ display: 'block', fontSize: '0.8rem', color: '#94a3b8', marginBottom: '0.35rem' }}>
              Categoria
            </label>
            <input
              type="text"
              value={filters.category}
              onChange={(e) => onFilterChange('category', e.target.value)}
              placeholder="es. nature, sport..."
              style={inputStyle}
            />
          </div>
        </div>

        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: '1rem' }}>
          <div style={{ fontSize: '0.85rem', color: '#94a3b8' }}>
            {activeFilterCount > 0 ? (
              <span>
                {activeFilterCount} {activeFilterCount === 1 ? 'filtro attivo' : 'filtri attivi'}
              </span>
            ) : (
              <span>Nessun filtro attivo</span>
            )}
          </div>
          <div style={{ display: 'flex', gap: '0.5rem' }}>
            {activeFilterCount > 0 && (
              <button onClick={onClearFilters} style={secondaryButtonStyle}>
                Cancella filtri
              </button>
            )}
            <div style={{ display: 'flex', border: '1px solid #334155', borderRadius: '6px', overflow: 'hidden' }}>
              <button
                onClick={() => onViewModeChange('table')}
                style={{
                  ...viewToggleStyle,
                  background: viewMode === 'table' ? '#38bdf8' : '#1e293b',
                  color: viewMode === 'table' ? '#0f172a' : '#e2e8f0',
                }}
              >
                Tabella
              </button>
              <button
                onClick={() => onViewModeChange('cards')}
                style={{
                  ...viewToggleStyle,
                  background: viewMode === 'cards' ? '#38bdf8' : '#1e293b',
                  color: viewMode === 'cards' ? '#0f172a' : '#e2e8f0',
                }}
              >
                Card
              </button>
            </div>
          </div>
        </div>
      </div>
    </>
  )
}
