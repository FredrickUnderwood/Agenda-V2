import { apiClient } from './client'
import type { User } from './types'

export interface LoginResponse {
  token: string
  user: User
}

export function login(username: string, password: string) {
  return apiClient.post<LoginResponse>('/auth/login', { username, password }).then((r) => r.data)
}

export function me() {
  return apiClient.get<User>('/auth/me').then((r) => r.data)
}
