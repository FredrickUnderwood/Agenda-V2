import { apiClient } from './client'
import type { CreateMachineRequest, ListResponse, Machine, UpdateMachineRequest } from './types'

export function listMachines() {
  return apiClient.get<ListResponse<Machine>>('/machines').then((r) => r.data)
}

export function getMachine(id: number) {
  return apiClient.get<Machine>(`/machines/${id}`).then((r) => r.data)
}

export function createMachine(req: CreateMachineRequest) {
  return apiClient.post<Machine>('/machines', req).then((r) => r.data)
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
