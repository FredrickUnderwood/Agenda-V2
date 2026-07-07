import axios from 'axios'
import { clearToken, getToken } from '@/auth/tokenStore'

export const apiClient = axios.create({
  baseURL: '/api/v1',
  timeout: 30_000,
})

apiClient.interceptors.request.use((config) => {
  const token = getToken()
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// The control plane issues a fixed-TTL JWT with no refresh endpoint (see
// internal/auth.Manager — Issue/Verify only) — so on 401 there's nothing to
// silently retry, just send the user back to log in again.
apiClient.interceptors.response.use(
  (res) => res,
  (error) => {
    if (error.response?.status === 401 && window.location.pathname !== '/login') {
      clearToken()
      window.location.href = '/login'
    }
    return Promise.reject(error)
  },
)
