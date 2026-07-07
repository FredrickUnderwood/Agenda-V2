import { apiClient } from './client'
import type { ListResponse, Setting, UpsertSettingRequest } from './types'

export function listSettings() {
  return apiClient.get<ListResponse<Setting>>('/settings').then((r) => r.data)
}

export function upsertSetting(key: string, req: UpsertSettingRequest) {
  return apiClient.put<Setting>(`/settings/${encodeURIComponent(key)}`, req).then((r) => r.data)
}

export function deleteSetting(key: string) {
  return apiClient.delete(`/settings/${encodeURIComponent(key)}`)
}
