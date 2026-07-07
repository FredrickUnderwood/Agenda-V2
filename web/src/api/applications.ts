import { apiClient } from './client'
import type {
  Application,
  ApplicationEnvTarget,
  ApplicationInstanceHealth,
  CreateApplicationRequest,
  Environment,
  ListResponse,
  NodeLogsResponse,
  UpdateApplicationRequest,
} from './types'

export function listApplications() {
  return apiClient.get<ListResponse<Application>>('/applications').then((r) => r.data)
}

export function getApplication(id: number) {
  return apiClient.get<Application>(`/applications/${id}`).then((r) => r.data)
}

export function createApplication(req: CreateApplicationRequest) {
  return apiClient.post<Application>('/applications', req).then((r) => r.data)
}

export function updateApplication(id: number, req: UpdateApplicationRequest) {
  return apiClient.put<Application>(`/applications/${id}`, req).then((r) => r.data)
}

export function deleteApplication(id: number) {
  return apiClient.delete(`/applications/${id}`)
}

export function listApplicationInstances(appId: number, env?: Environment) {
  return apiClient
    .get<ListResponse<ApplicationEnvTarget>>(`/applications/${appId}/instances`, { params: { env } })
    .then((r) => r.data)
}

export function getInstanceHealth(appId: number, targetId: number) {
  return apiClient
    .get<ApplicationInstanceHealth | { status: 'unknown' }>(`/applications/${appId}/instances/${targetId}/health`)
    .then((r) => r.data)
}

export function checkInstanceHealth(appId: number, targetId: number) {
  return apiClient
    .post<ApplicationInstanceHealth>(`/applications/${appId}/instances/${targetId}/health/check`)
    .then((r) => r.data)
}

export function getInstanceLogs(appId: number, targetId: number, opts?: { service?: string; tail?: number }) {
  return apiClient
    .get<NodeLogsResponse>(`/applications/${appId}/instances/${targetId}/logs`, { params: opts })
    .then((r) => r.data)
}
