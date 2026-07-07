import { createContext, useContext, useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import * as authApi from '@/api/auth'
import type { User } from '@/api/types'
import { clearToken, getToken, setToken } from './tokenStore'

interface AuthContextValue {
  user: User | null
  loading: boolean
  login: (username: string, password: string) => Promise<void>
  logout: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!getToken()) {
      setLoading(false)
      return
    }
    authApi
      .me()
      .then(setUser)
      .catch(() => clearToken())
      .finally(() => setLoading(false))
  }, [])

  async function login(username: string, password: string) {
    const res = await authApi.login(username, password)
    setToken(res.token)
    setUser(res.user)
  }

  function logout() {
    clearToken()
    setUser(null)
    window.location.href = '/login'
  }

  return <AuthContext.Provider value={{ user, loading, login, logout }}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
