import { apiClient } from './client'
import type { Environment, ListResponse, MachineFile, UploadEnvFileResponse } from './types'

export function listApplicationEnvFiles(appId: number, env?: Environment) {
  return apiClient
    .get<ListResponse<MachineFile>>(`/applications/${appId}/files`, { params: { env } })
    .then((r) => r.data)
}

// Uploads the same content to every machine hosting the environment. The
// browser streams it once per request; the control plane fans it out.
export function uploadApplicationEnvFile(
  appId: number,
  params: { env: Environment; file: File; fileName?: string; mode?: string; overwrite?: boolean },
) {
  const form = new FormData()
  form.append('file', params.file)
  form.append('env', params.env)
  if (params.fileName) form.append('file_name', params.fileName)
  if (params.mode) form.append('mode', params.mode)
  if (params.overwrite) form.append('overwrite', 'true')
  return apiClient
    .post<UploadEnvFileResponse>(`/applications/${appId}/files`, form, {
      // An upload of any real size outlives the client's default timeout, and
      // aborting it halfway leaves nothing useful behind.
      timeout: 0,
    })
    .then((r) => r.data)
}

export function listMachineFiles(machineId: number) {
  return apiClient.get<ListResponse<MachineFile>>(`/machines/${machineId}/files`).then((r) => r.data)
}

export function uploadMachineFile(
  machineId: number,
  params: { path: string; file: File; mode?: string; overwrite?: boolean },
) {
  const form = new FormData()
  form.append('file', params.file)
  form.append('path', params.path)
  if (params.mode) form.append('mode', params.mode)
  if (params.overwrite) form.append('overwrite', 'true')
  return apiClient
    .post<MachineFile>(`/machines/${machineId}/files`, form, { timeout: 0 })
    .then((r) => r.data)
}

// Re-reads the file on its machine and returns the record with a fresh
// verification result.
export function verifyMachineFile(fileId: number) {
  return apiClient.post<MachineFile>(`/files/${fileId}/verify`).then((r) => r.data)
}
