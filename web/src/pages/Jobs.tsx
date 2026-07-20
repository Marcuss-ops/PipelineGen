import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { listJobs, JobSummary } from '../api/client'

const statusColors: Record<string, string> = {
  SUCCEEDED: '#34d399',
  COMPLETED: '#34d399',
  FAILED: '#f87171',
  CANCELLED: '#94a3b8',
  RUNNING: '#38bdf8',
  LEASED: '#818cf8',
  QUEUED: '#fbbf24',
  PENDING: '#fbbf24',
  WAITING_CHILDREN: '#a78bfa',
  FINALIZING: '#60a5fa',
  PARTIALLY_SUCCEEDED: '#f472b6',
}

export default function Jobs() {
  const [jobs, setJobs] = useState<JobSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [typeFilter, setTypeFilter] = useState('')

  useEffect(() => {
    setLoading(true)
    setError(null)
    listJobs({ limit: 200 })
      .then((res) => setJobs(res.jobs || []))
      .catch((err) => setError(err instanceof Error ? err.message : 'Errore caricamento job'))
      .finally(() => setLoading(false))
  }, [])

  const filtered = jobs.filter((j) => {
    const matchesSearch =
      !search ||
      j.id.toLowerCase().includes(search.toLowerCase()) ||
      j.type.toLowerCase().includes(search.toLowerCase()) ||
      (j.correlation_id && j.correlation_id.toLowerCase().includes(search.toLowerCase()))
    const matchesStatus = !statusFilter || j.status === statusFilter
    const matchesType = !typeFilter || j.type === typeFilter
    return matchesSearch && matchesStatus && matchesType
  })

  const statuses = Array.from(new Set(jobs.map((j) => j.status))).sort()
  const types = Array.from(new Set(jobs.map((j) => j.type))).sort()

  return (
    <div style={{ padding: '2rem' }}>
      <h2 style={{ margin: '0 0 1.5rem', fontSize: '1.75rem', color: '#e2e8f0' }}>Jobs</h2>

      <div
        style={{
          display: 'flex',
          flexWrap: 'wrap',
          gap: '0.75rem',
          marginBottom: '1.5rem',
          padding: '1rem',
          background: '#1e293b',
          border: '1px solid #334155',
          borderRadius: '8px',
        }}
      >
        <input
          type="text"
          placeholder="Cerca ID, tipo, correlation ID..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          style={{ ...inputStyle, minWidth: '260px', flex: 1 }}
        />
        <select value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)} style={selectStyle}>
          <option value="">Tutti gli stati</option>
          {statuses.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <select value={typeFilter} onChange={(e) => setTypeFilter(e.target.value)} style={selectStyle}>
          <option value="">Tutti i tipi</option>
          {types.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
      </div>

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

      {loading ? (
        <div style={{ color: '#94a3b8', textAlign: 'center', padding: '2rem' }}>Caricamento job...</div>
      ) : (
        <>
          <div style={{ marginBottom: '1rem', color: '#94a3b8', fontSize: '0.9rem' }}>
            {filtered.length} job su {jobs.length}
          </div>
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
              <thead>
                <tr style={{ borderBottom: '2px solid #334155' }}>
                  <th style={thStyle}>ID</th>
                  <th style={thStyle}>Tipo</th>
                  <th style={thStyle}>Stato</th>
                  <th style={thStyle}>Progresso</th>
                  <th style={thStyle}>Correlation ID</th>
                  <th style={thStyle}>Creato</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((j) => (
                  <tr key={j.id} style={{ borderBottom: '1px solid #334155' }}>
                    <td style={tdStyle}>
                      <Link to={`/jobs/${encodeURIComponent(j.id)}`} style={{ color: '#38bdf8', textDecoration: 'none' }}>
                        {j.id.slice(0, 24)}...
                      </Link>
                    </td>
                    <td style={tdStyle}>{j.type}</td>
                    <td style={tdStyle}>
                      <span
                        style={{
                          display: 'inline-block',
                          padding: '0.25rem 0.5rem',
                          borderRadius: '9999px',
                          background: `${statusColors[j.status] || '#94a3b8'}22`,
                          color: statusColors[j.status] || '#94a3b8',
                          fontWeight: 600,
                          fontSize: '0.8rem',
                        }}
                      >
                        {j.status}
                      </span>
                    </td>
                    <td style={tdStyle}>
                      <div style={{ background: '#334155', borderRadius: '4px', height: '6px', width: '100px' }}>
                        <div
                          style={{
                            background: '#38bdf8',
                            height: '6px',
                            borderRadius: '4px',
                            width: `${Math.min(100, Math.max(0, j.progress || 0))}%`,
                          }}
                        />
                      </div>
                      <span style={{ color: '#94a3b8', fontSize: '0.75rem' }}>{j.progress || 0}%</span>
                    </td>
                    <td style={tdStyle}>{j.correlation_id || '-'}</td>
                    <td style={tdStyle}>
                      {j.created_at ? new Date(j.created_at).toLocaleString('it-IT') : '-'}
                    </td>
                  </tr>
                ))}
                {filtered.length === 0 && (
                  <tr>
                    <td colSpan={6} style={{ textAlign: 'center', padding: '2rem', color: '#94a3b8' }}>
                      Nessun job trovato.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </>
      )}
    </div>
  )
}

const inputStyle: React.CSSProperties = {
  padding: '0.55rem 0.75rem',
  background: '#0f172a',
  color: '#e2e8f0',
  border: '1px solid #334155',
  borderRadius: '6px',
  fontSize: '0.9rem',
}

const selectStyle: React.CSSProperties = {
  ...inputStyle,
  minWidth: '160px',
}

const thStyle: React.CSSProperties = {
  textAlign: 'left',
  padding: '0.75rem 1rem',
  color: '#94a3b8',
  fontWeight: 600,
  fontSize: '0.8rem',
  textTransform: 'uppercase',
}

const tdStyle: React.CSSProperties = {
  padding: '0.85rem 1rem',
  color: '#e2e8f0',
  verticalAlign: 'top',
}
