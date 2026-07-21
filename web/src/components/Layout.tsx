import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'

interface NavItem {
  to: string
  label: string
  icon: string
}

const navItems: NavItem[] = [
  { to: '/', label: 'Overview', icon: '◎' },
  { to: '/content', label: 'Content Library', icon: '☰' },
  { to: '/database', label: 'Database', icon: '▦' },
  { to: '/jobs', label: 'Jobs', icon: '⚙' },
  { to: '/operations', label: 'Operations', icon: '◉' },
]

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const location = useLocation()
  const navigate = useNavigate()
  const { logout } = useAuth()

  return (
    <div style={{ display: 'flex', minHeight: '100vh', background: '#0f172a', color: '#e2e8f0' }}>
      {/* Sidebar */}
      <aside
        style={{
          width: '260px',
          background: '#1e293b',
          borderRight: '1px solid #334155',
          display: 'flex',
          flexDirection: 'column',
          position: 'fixed',
          top: 0,
          left: 0,
          bottom: 0,
          zIndex: 50,
        }}
      >
        <div style={{ padding: '1.5rem', borderBottom: '1px solid #334155' }}>
          <h1 style={{ margin: 0, fontSize: '1.25rem', color: '#38bdf8', fontWeight: 700 }}>
            PipelineGen
          </h1>
          <p style={{ margin: '0.25rem 0 0', fontSize: '0.75rem', color: '#94a3b8' }}>
            Admin Dashboard
          </p>
        </div>

        <nav style={{ flex: 1, padding: '1rem 0' }}>
          {navItems.map((item) => {
            const isActive = location.pathname === item.to || location.pathname.startsWith(`${item.to}/`)
            return (
              <Link
                key={item.to}
                to={item.to}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '0.75rem',
                  padding: '0.85rem 1.5rem',
                  color: isActive ? '#38bdf8' : '#94a3b8',
                  background: isActive ? 'rgba(56,189,248,0.1)' : 'transparent',
                  borderLeft: isActive ? '3px solid #38bdf8' : '3px solid transparent',
                  textDecoration: 'none',
                  fontSize: '0.95rem',
                  fontWeight: 500,
                  transition: 'all 0.15s ease',
                }}
              >
                <span style={{ fontSize: '1.1rem' }}>{item.icon}</span>
                {item.label}
              </Link>
            )
          })}
        </nav>

        <div style={{ padding: '1rem 1.5rem', borderTop: '1px solid #334155' }}>
          <button
            onClick={async () => {
              await logout()
              navigate('/login')
            }}
            style={{
              width: '100%',
              padding: '0.55rem 0.75rem',
              background: 'transparent',
              color: '#94a3b8',
              border: '1px solid #334155',
              borderRadius: '6px',
              cursor: 'pointer',
              fontSize: '0.85rem',
              fontWeight: 500,
            }}
          >
            Logout
          </button>
          <div style={{ marginTop: '0.75rem', fontSize: '0.75rem', color: '#64748b', textAlign: 'center' }}>
            v0.2.0 · Fase 2
          </div>
        </div>
      </aside>

      {/* Main content */}
      <main style={{ flex: 1, marginLeft: '260px', minHeight: '100vh' }}>
        {children}
      </main>
    </div>
  )
}
