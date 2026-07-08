import { apiClient } from './client'
import type { CreateMachineRequest, ListResponse, Machine, MachineCreateResult, RotateTokenResult, UpdateMachineRequest } from './types'

export function listMachines() {
  return apiClient.get<ListResponse<Machine>>('/machines').then((r) => r.data)
}

export function getMachine(id: number) {
  return apiClient.get<Machine>(`/machines/${id}`).then((r) => r.data)
}

export function createMachine(req: CreateMachineRequest) {
  return apiClient.post<MachineCreateResult>('/machines', req).then((r) => r.data)
}

export function updateMachine(id: number, req: UpdateMachineRequest) {
  return apiClient.put<Machine>(`/machines/${id}`, req).then((r) => r.data)
}

export function deleteMachine(id: number) {
  return apiClient.delete(`/machines/${id}`)
}

export function testMachineConnection(id: number) {
  return apiClient.post<{ ok: boolean; error?: string }>(`/machines/${id}/test`).then((r) => r.data)
}

export function rotateMachineToken(id: number) {
  return apiClient.post<RotateTokenResult>(`/machines/${id}/rotate-token`).then((r) => r.data)
}
