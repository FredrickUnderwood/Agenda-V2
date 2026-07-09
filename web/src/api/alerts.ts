import { apiClient } from './client'
import type { AlertChannel } from './types'

export function listAlertChannels() {
  return apiClient.get<AlertChannel[]>('/alerts/channels').then((r) => r.data)
}
