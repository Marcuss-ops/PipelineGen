import { useState } from 'react'
import { useAuth } from '../context/AuthContext'

export default function Login() {
  const { login } = useAuth()
  const [token, setToken] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)
    if (!token.trim()) {
      setError('Inserisci il token admin')
      return
    }
    setLoading(true)
    try {
      await login(token.trim())
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login fallito')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: '#0f172a',
        padding: '1rem',
      }}
    >
      <div
        style={{
          width: '100%',
          maxWidth: '420px',
          background: '#1e293b',
          border: '1px solid #334155',
          borderRadius: '12px',
          padding: '2rem',
          boxShadow: '0 20px 40px rgba(0,0,0,0.3)',
        }}
      >
        <div style={{ textAlign: 'center', marginBottom: '1.5rem' }}>
          <h1 style={{ margin: 0, fontSize: '1.75rem', color: '#38bdf8' }}>PipelineGen</h1>
          <p style={{ margin: '0.5rem 0 0', color: '#94a3b8' }}>Admin Dashboard</p>
        </div>

        <form onSubmit={handleSubmit}>
          <label
            htmlFor="token"
            style={{
              display: 'block',
              marginBottom: '0.5rem',
              color: '#e2e8f0',
              fontSize: '0.9rem',
              fontWeight: 500,
            }}
          >
            Token admin
          </label>
          <input
            id="token"
            type="password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder="Incolla il tuo token admin"
            autoComplete="off"
            style={{
              width: '100%',
              padding: '0.75rem 1rem',
              background: '#0f172a',
              border: '1px solid #334155',
              borderRadius: '8px',
              color: '#e2e8f0',
              fontSize: '0.95rem',
              boxSizing: 'border-box',
              marginBottom: '1rem',
            }}
          />

          {error && (
            <div
              style={{
                background: 'rgba(248,113,113,0.1)',
                border: '1px solid #f87171',
                color: '#f87171',
                padding: '0.75rem 1rem',
                borderRadius: '8px',
                marginBottom: '1rem',
                fontSize: '0.9rem',
              }}
            >
              {error}
            </div>
          )}

          <button
            type="submit"
            disabled={loading}
            style={{
              width: '100%',
              padding: '0.85rem',
              background: loading ? '#334155' : '#38bdf8',
              color: '#0f172a',
              border: 'none',
              borderRadius: '8px',
              fontSize: '1rem',
              fontWeight: 600,
              cursor: loading ? 'not-allowed' : 'pointer',
            }}
          >
            {loading ? 'Accesso in corso...' : 'Accedi'}
          </button>
        </form>

        <p style={{ marginTop: '1.25rem', fontSize: '0.8rem', color: '#64748b', textAlign: 'center' }}>
          Il token viene inviato al server che imposta un cookie di sessione HTTP-only.
        </p>
      </div>
    </div>
  )
}
