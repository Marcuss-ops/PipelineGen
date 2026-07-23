import { BulkAction, BulkChange, BulkOperationResponse } from '../../../api/assets'
import { BULK_ACTIONS, REVIEW_STATUSES } from './constants'
import { inputStyle, primaryButtonStyle, secondaryButtonStyle, previewThStyle, previewTdStyle, previewPreStyle } from './styles'

interface AssetBulkDialogProps {
  selectedCount: number
  bulkAction: BulkAction | ''
  bulkPayloadValue: string
  bulkReviewStatus: string
  bulkLoading: boolean
  bulkPreview: BulkOperationResponse | null
  bulkError: string | null
  bulkSuccess: string | null
  needsPayloadInput: boolean
  canPreview: boolean
  onActionChange: (action: BulkAction | '') => void
  onPayloadChange: (value: string) => void
  onReviewStatusChange: (value: string) => void
  onPreview: () => void
  onRun: () => void
  onClose: () => void
}

export function AssetBulkDialog({
  selectedCount,
  bulkAction,
  bulkPayloadValue,
  bulkReviewStatus,
  bulkLoading,
  bulkPreview,
  bulkError,
  bulkSuccess,
  needsPayloadInput,
  canPreview,
  onActionChange,
  onPayloadChange,
  onReviewStatusChange,
  onPreview,
  onRun,
  onClose,
}: AssetBulkDialogProps) {
  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0,0,0,0.7)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 100,
        padding: '1rem',
      }}
      onClick={onClose}
    >
      <div
        style={{
          background: '#0f172a',
          border: '1px solid #334155',
          borderRadius: '12px',
          width: '100%',
          maxWidth: 720,
          maxHeight: '90vh',
          overflow: 'auto',
          padding: '1.5rem',
        }}
        onClick={(e) => e.stopPropagation()}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '1rem' }}>
          <h3 style={{ margin: 0, color: '#e2e8f0' }}>Azioni bulk</h3>
          <button onClick={onClose} style={{ ...secondaryButtonStyle, padding: '0.35rem 0.75rem' }}>
            Chiudi
          </button>
        </div>

        <p style={{ color: '#94a3b8', marginTop: 0 }}>
          {selectedCount} {selectedCount === 1 ? 'asset selezionato' : 'asset selezionati'}.
          Scegli un'azione e verifica il preview prima di eseguire.
        </p>

        <div style={{ marginBottom: '1rem' }}>
          <label style={{ display: 'block', fontSize: '0.8rem', color: '#94a3b8', marginBottom: '0.35rem' }}>
            Azione
          </label>
          <select
            value={bulkAction}
            onChange={(e) => onActionChange(e.target.value as BulkAction)}
            style={inputStyle}
          >
            <option value="">Seleziona azione</option>
            {BULK_ACTIONS.map((a) => (
              <option key={a.key} value={a.key}>
                {a.label}
              </option>
            ))}
          </select>
        </div>

        {needsPayloadInput && (
          <div style={{ marginBottom: '1rem' }}>
            <label style={{ display: 'block', fontSize: '0.8rem', color: '#94a3b8', marginBottom: '0.35rem' }}>
              {bulkAction === 'set_category' ? 'Nuova categoria' : 'Tag (separati da virgola)'}
            </label>
            <input
              type="text"
              value={bulkPayloadValue}
              onChange={(e) => onPayloadChange(e.target.value)}
              placeholder={bulkAction === 'set_category' ? 'es. sport' : 'es. boxing, training'}
              style={inputStyle}
            />
          </div>
        )}

        {bulkAction === 'set_review_status' && (
          <div style={{ marginBottom: '1rem' }}>
            <label style={{ display: 'block', fontSize: '0.8rem', color: '#94a3b8', marginBottom: '0.35rem' }}>
              Review status
            </label>
            <select
              value={bulkReviewStatus}
              onChange={(e) => onReviewStatusChange(e.target.value)}
              style={inputStyle}
            >
              {REVIEW_STATUSES.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </div>
        )}

        <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '1rem', flexWrap: 'wrap' }}>
          <button onClick={onPreview} disabled={!canPreview || bulkLoading} style={secondaryButtonStyle}>
            {bulkLoading && bulkPreview === null ? 'Preview...' : '🔍 Preview dry-run'}
          </button>
          <button
            onClick={onRun}
            disabled={!canPreview || bulkLoading || !bulkPreview}
            style={primaryButtonStyle}
          >
            Esegui
          </button>
        </div>

        {bulkError && (
          <div
            style={{
              background: 'rgba(248,113,113,0.1)',
              border: '1px solid #f87171',
              color: '#f87171',
              padding: '0.75rem',
              borderRadius: '6px',
              marginBottom: '1rem',
            }}
          >
            {bulkError}
          </div>
        )}

        {bulkSuccess && (
          <div
            style={{
              background: 'rgba(74,222,128,0.1)',
              border: '1px solid #4ade80',
              color: '#4ade80',
              padding: '0.75rem',
              borderRadius: '6px',
              marginBottom: '1rem',
            }}
          >
            {bulkSuccess}
          </div>
        )}

        {bulkPreview && (
          <div
            style={{
              background: '#1e293b',
              border: '1px solid #334155',
              borderRadius: '8px',
              padding: '1rem',
              marginBottom: '1rem',
            }}
          >
            <div style={{ color: '#e2e8f0', fontWeight: 600, marginBottom: '0.5rem' }}>
              {bulkPreview.dry_run ? 'Preview dry-run' : 'Risultato esecuzione'}
            </div>
            <div style={{ color: '#94a3b8', fontSize: '0.85rem', marginBottom: '0.75rem' }}>
              Azione: <strong>{bulkPreview.action}</strong> | Successo: {bulkPreview.affected} | Fallimenti: {bulkPreview.failed}
            </div>
            <div style={{ maxHeight: 300, overflow: 'auto' }}>
              <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.85rem' }}>
                <thead>
                  <tr style={{ textAlign: 'left', color: '#94a3b8' }}>
                    <th style={previewThStyle}>Asset</th>
                    <th style={previewThStyle}>Stato</th>
                    <th style={previewThStyle}>Prima</th>
                    <th style={previewThStyle}>Dopo</th>
                    <th style={previewThStyle}>Messaggio</th>
                  </tr>
                </thead>
                <tbody>
                  {bulkPreview.changes.map((change) => (
                    <PreviewRow key={change.asset_id} change={change} />
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

function PreviewRow({ change }: { change: BulkChange }) {
  return (
    <tr style={{ borderBottom: '1px solid #334155' }}>
      <td style={previewTdStyle}>{change.asset_id}</td>
      <td style={previewTdStyle}>
        <span style={{ color: change.status === 'success' ? '#4ade80' : '#f87171' }}>
          {change.status}
        </span>
      </td>
      <td style={previewTdStyle}>
        <pre style={previewPreStyle}>{JSON.stringify(change.before, null, 2)}</pre>
      </td>
      <td style={previewTdStyle}>
        <pre style={previewPreStyle}>{JSON.stringify(change.after, null, 2)}</pre>
      </td>
      <td style={previewTdStyle}>{change.message || '-'}</td>
    </tr>
  )
}
