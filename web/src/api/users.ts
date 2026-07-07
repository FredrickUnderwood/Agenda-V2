import { apiClient } from './client'
import type { CreateUserRequest, ListResponse, User } from './types'

export function listUsers() {
  return apiClient.get<ListResponse<User>>('/users').then((r) => r.data)
}

export function createUser(req: CreateUserRequest) {
  return apiClient.post<User>('/users', req).then((r) => r.data)
}

export function deleteUser(id: number) {
  return apiClient.delete(`/users/${id}`)
}
