import { Navigate, Outlet } from 'react-router-dom'
import { Spin } from 'antd'
import { useAuth } from './AuthContext'
import { getToken } from './tokenStore'

export function PrivateRoute() {
  const { loading } = useAuth()
  if (!getToken()) return <Navigate to="/login" replace />
  if (loading) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100vh' }}>
        <Spin size="large" />
      </div>
    )
  }
  return <Outlet />
}
