import { BrowserRouter, Routes, Route, Navigate, Outlet } from 'react-router-dom'
import { AuthProvider, useAuth } from './context/AuthContext'
import AdminLayout from './components/Layout'
import Overview from './pages/Overview'
import ContentLibrary from './pages/ContentLibrary'
import AssetInspector from './pages/AssetInspector'
import Jobs from './pages/Jobs'
import Indexing from './pages/Indexing'
import Login from './pages/Login'

function ProtectedRoutes() {
  const { isAuthenticated, isLoading } = useAuth()

  if (isLoading) {
    return (
      <div
        style={{
          minHeight: '100vh',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          background: '#0f172a',
          color: '#94a3b8',
        }}
      >
        Verifica sessione in corso...
      </div>
    )
  }

  if (!isAuthenticated) {
    return <Navigate to="/login" replace />
  }

  return (
    <AdminLayout>
      <Outlet />
    </AdminLayout>
  )
}

function PublicLogin() {
  const { isAuthenticated, isLoading } = useAuth()
  if (isLoading) {
    return (
      <div
        style={{
          minHeight: '100vh',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          background: '#0f172a',
          color: '#94a3b8',
        }}
      >
        Verifica sessione in corso...
      </div>
    )
  }
  if (isAuthenticated) {
    return <Navigate to="/" replace />
  }
  return <Login />
}

function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<PublicLogin />} />
      <Route element={<ProtectedRoutes />}>
        <Route path="/" element={<Overview />} />
        <Route path="/content" element={<ContentLibrary />} />
        <Route path="/content/:id" element={<AssetInspector />} />
        <Route path="/jobs" element={<Jobs />} />
        <Route path="/indexing" element={<Indexing />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

function App() {
  return (
    <BrowserRouter basename="/admin">
      <AuthProvider>
        <AppRoutes />
      </AuthProvider>
    </BrowserRouter>
  )
}

export default App
