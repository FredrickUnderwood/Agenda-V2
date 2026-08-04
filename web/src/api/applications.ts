import { apiClient } from './client'
import type {
  Application,
  ApplicationEnvTarget,
  ApplicationInstanceHealth,
  CreateApplicationRequest,
  DeployLog,
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

// decommissionInstance drains the instance's gateway traffic and tears its
// containers down (DesiredState=stopped). Returns 202 with the teardown
// DeployLog, whose steps run asynchronously — poll the deploy log for progress.
export function decommissionInstance(appId: number, targetId: number) {
  return apiClient
    .post<DeployLog>(`/applications/${appId}/instances/${targetId}/decommission`)
    .then((r) => r.data)
}

// recommissionInstance clears the stopped intent so the instance rejoins the
// deploy set; it does not start containers (trigger a deploy for that).
export function recommissionInstance(appId: number, targetId: number) {
  return apiClient
    .post<ApplicationEnvTarget>(`/applications/${appId}/instances/${targetId}/recommission`)
    .then((r) => r.data)
}
