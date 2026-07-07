import { apiClient } from './client'
import type { ApplicationRelease, CreateReleaseRequest, DeployLog, Environment, ListResponse, ReleaseStatus } from './types'

export function listReleases(
  appId: number,
  filter?: { env?: Environment; instance_name?: string; status?: ReleaseStatus; limit?: number; offset?: number },
) {
  return apiClient
    .get<ListResponse<ApplicationRelease>>(`/applications/${appId}/releases`, { params: filter })
    .then((r) => r.data)
}

export function createRelease(appId: number, req: CreateReleaseRequest) {
  return apiClient.post<ApplicationRelease>(`/applications/${appId}/releases`, req).then((r) => r.data)
}

export function getRelease(id: number) {
  return apiClient
    .get<{ release: ApplicationRelease; deploy_log?: DeployLog }>(`/releases/${id}`)
    .then((r) => r.data)
}

export function deployRelease(id: number) {
  return apiClient.post<DeployLog>(`/releases/${id}/deploy`).then((r) => r.data)
}

export function retryRelease(id: number, fromStep: number) {
  return apiClient.post<DeployLog>(`/releases/${id}/retry`, { from_step: fromStep }).then((r) => r.data)
}

export function pauseRelease(id: number) {
  return apiClient.post<{ paused: boolean }>(`/releases/${id}/pause`).then((r) => r.data)
}

export function resumeRelease(id: number) {
  return apiClient.post<DeployLog>(`/releases/${id}/resume`).then((r) => r.data)
}

export function verifyRelease(id: number) {
  return apiClient.post<ApplicationRelease>(`/releases/${id}/verify`).then((r) => r.data)
}

export function rollbackRelease(id: number, targetReleaseId: number) {
  return apiClient
    .post<ApplicationRelease>(`/releases/${id}/rollback`, { target_release_id: targetReleaseId })
    .then((r) => r.data)
}
