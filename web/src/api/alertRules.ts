import { apiClient } from './client'
import type { AlertRule, AlertRuleTestResult, ListResponse, UpsertAlertRuleRequest } from './types'

export function listAlertRules() {
  return apiClient.get<ListResponse<AlertRule>>('/alert-rules').then((r) => r.data)
}

export function createAlertRule(req: UpsertAlertRuleRequest) {
  return apiClient.post<AlertRule>('/alert-rules', req).then((r) => r.data)
}

export function updateAlertRule(id: number, req: UpsertAlertRuleRequest) {
  return apiClient.put<AlertRule>(`/alert-rules/${id}`, req).then((r) => r.data)
}

export function deleteAlertRule(id: number) {
  return apiClient.delete(`/alert-rules/${id}`)
}

export function testAlertRule(id: number) {
  return apiClient.post<AlertRuleTestResult>(`/alert-rules/${id}/test`).then((r) => r.data)
}
