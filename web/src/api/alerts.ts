import { apiClient } from './client'
import type { AlertChannel } from './types'

export function listAlertChannels() {
  return apiClient.get<AlertChannel[]>('/alerts/channels').then((r) => r.data)
}

export interface TestAlertRequest {
  type: string
  webhook_url: string
  secret?: string
}

// Sends one ad-hoc message to a channel supplied in the request body (not
// persisted) so an operator can validate a webhook before saving it. The backend
// reports downstream failures as { ok:false, error } inside a 200 — see
// internal/handler/alert_handler.go testAlert.
export function testAlert(req: TestAlertRequest) {
  return apiClient.post<{ ok: boolean; error?: string }>('/alerts/test', req).then((r) => r.data)
}
