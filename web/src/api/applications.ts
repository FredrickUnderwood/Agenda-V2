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
// Bringing the instance back is a normal deploy (there is no recommission): the
// deploy restarts it and clears the stopped state on success.
export function decommissionInstance(appId: number, targetId: number) {
  return apiClient
    .post<DeployLog>(`/applications/${appId}/instances/${targetId}/decommission`)
    .then((r) => r.data)
}

// deleteInstance permanently removes a decommissioned instance's record, along
// with its health row and any gateway route pins naming it. Deploy logs and
// releases are kept as history. The API refuses (409) unless the instance has
// been decommissioned first — see the backend's DeleteInstance for why.
export function deleteInstance(appId: number, targetId: number) {
  return apiClient.delete<void>(`/applications/${appId}/instances/${targetId}`).then((r) => r.data)
}
