import { useState } from 'react'
import { useAssetInventory } from './useAssetInventory'
import { useAssetSelection } from './useAssetSelection'
import { useAssetBulkActions } from './useAssetBulkActions'
import { AssetFilterBar } from './AssetFilterBar'
import { AssetInventoryTable } from './AssetInventoryTable'
import { AssetInventoryCards } from './AssetInventoryCards'
import { AssetBulkToolbar } from './AssetBulkToolbar'
import { AssetBulkDialog } from './AssetBulkDialog'
import { AssetPagination } from './AssetPagination'

export default function ContentLibraryPage() {
  const [viewMode, setViewMode] = useState<'table' | 'cards'>('table')
  const {
    assets,
    loading,
    error,
    hasMore,
    facets,
    facetsError,
    filters,
    activeFilterCount,
    handleFilterChange,
    clearFilters,
    handleLoadMore,
  } = useAssetInventory()

  const { selected, toggleSelect, toggleSelectAll, clearSelection } = useAssetSelection()

  const {
    bulkAction,
    bulkPayloadValue,
    bulkReviewStatus,
    bulkLoading,
    bulkPreview,
    bulkError,
    bulkSuccess,
    showBulkModal,
    needsPayloadInput,
    openBulkModal,
    closeBulkModal,
    setBulkAction,
    setBulkPayloadValue,
    setBulkReviewStatus,
    canPreview,
    runBulk,
  } = useAssetBulkActions()

  const handleToggleAll = () => {
    toggleSelectAll(assets)
  }

  const handlePreview = () => {
    runBulk(true, selected)
  }

  const handleRun = () => {
    runBulk(false, selected)
  }

  return (
    <div style={{ padding: '2rem' }}>
      <div style={{ marginBottom: '1.5rem' }}>
        <h2 style={{ margin: 0, fontSize: '1.75rem', color: '#e2e8f0' }}>Content Library</h2>
        <p style={{ margin: '0.5rem 0 0', color: '#94a3b8' }}>
          Esplora tutti gli asset indicizzati nel sistema.
        </p>
      </div>

      <AssetFilterBar
        filters={filters}
        facets={facets}
        facetsError={facetsError}
        activeFilterCount={activeFilterCount}
        viewMode={viewMode}
        onFilterChange={handleFilterChange}
        onClearFilters={clearFilters}
        onViewModeChange={setViewMode}
      />

      <AssetBulkToolbar
        selectedCount={selected.size}
        onClear={clearSelection}
        onOpen={openBulkModal}
      />

      {error && (
        <div
          style={{
            background: 'rgba(248,113,113,0.1)',
            border: '1px solid #f87171',
            color: '#f87171',
            padding: '1rem',
            borderRadius: '8px',
            marginBottom: '1rem',
          }}
        >
          {error}
        </div>
      )}

      {loading && assets.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '3rem', color: '#94a3b8' }}>Caricamento asset...</div>
      ) : assets.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '3rem', color: '#94a3b8' }}>
          Nessun asset trovato con i filtri selezionati.
        </div>
      ) : viewMode === 'table' ? (
        <AssetInventoryTable
          assets={assets}
          selected={selected}
          onToggle={toggleSelect}
          onToggleAll={handleToggleAll}
        />
      ) : (
        <AssetInventoryCards assets={assets} selected={selected} onToggle={toggleSelect} />
      )}

      <AssetPagination hasMore={hasMore} loading={loading} onLoadMore={handleLoadMore} />

      {showBulkModal && (
        <AssetBulkDialog
          selectedCount={selected.size}
          bulkAction={bulkAction}
          bulkPayloadValue={bulkPayloadValue}
          bulkReviewStatus={bulkReviewStatus}
          bulkLoading={bulkLoading}
          bulkPreview={bulkPreview}
          bulkError={bulkError}
          bulkSuccess={bulkSuccess}
          needsPayloadInput={needsPayloadInput}
          canPreview={canPreview(selected)}
          onActionChange={setBulkAction}
          onPayloadChange={setBulkPayloadValue}
          onReviewStatusChange={setBulkReviewStatus}
          onPreview={handlePreview}
          onRun={handleRun}
          onClose={closeBulkModal}
        />
      )}
    </div>
  )
}
