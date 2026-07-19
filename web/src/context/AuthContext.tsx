import { createContext, useContext, useEffect, useState, ReactNode } from 'react'
import { checkAuth, login as apiLogin, logout as apiLogout } from '../api/client'

interface AuthContextValue {
  isAuthenticated: boolean
  isLoading: boolean
  login: (token: string) => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [isAuthenticated, setIsAuthenticated] = useState(false)
  const [isLoading, setIsLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    checkAuth()
      .then(() => {
        if (!cancelled) setIsAuthenticated(true)
      })
      .catch(() => {
        if (!cancelled) setIsAuthenticated(false)
      })
      .finally(() => {
        if (!cancelled) setIsLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  const login = async (token: string) => {
    await apiLogin(token)
    setIsAuthenticated(true)
  }

  const logout = async () => {
    try {
      await apiLogout()
    } catch {
      // Ignore logout errors and clear local state anyway.
    }
    setIsAuthenticated(false)
  }

  const value: AuthContextValue = {
    isAuthenticated,
    isLoading,
    login,
    logout,
  }

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const context = useContext(AuthContext)
  if (!context) {
    throw new Error('useAuth must be used within an AuthProvider')
  }
  return context
}
