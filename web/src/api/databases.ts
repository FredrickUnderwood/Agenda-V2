import { apiClient } from './client'
import type {
  CreateDatabaseInstanceRequest,
  DatabaseInstance,
  DBQueryLog,
  DBQueryLogDetail,
  ListResponse,
  QueryRequest,
  QueryResult,
  TestDatabaseInstanceResult,
  UpdateDatabaseInstanceRequest,
} from './types'

export function listDatabaseInstances() {
  return apiClient.get<ListResponse<DatabaseInstance>>('/db-instances').then((r) => r.data)
}

export function createDatabaseInstance(req: CreateDatabaseInstanceRequest) {
  return apiClient.post<DatabaseInstance>('/db-instances', req).then((r) => r.data)
}

export function updateDatabaseInstance(id: number, req: UpdateDatabaseInstanceRequest) {
  return apiClient.put<DatabaseInstance>(`/db-instances/${id}`, req).then((r) => r.data)
}

export function deleteDatabaseInstance(id: number) {
  return apiClient.delete(`/db-instances/${id}`)
}

export function testDatabaseInstance(id: number) {
  return apiClient.post<TestDatabaseInstanceResult>(`/db-instances/${id}/test`).then((r) => r.data)
}

// A statement can legitimately run longer than the client's default timeout,
// so this call gets its own budget: the server caps a query at 60s, and the
// request must outlive that rather than abandoning a query still running.
export function runQuery(id: number, req: QueryRequest) {
  return apiClient.post<QueryResult>(`/db-instances/${id}/query`, req, { timeout: 90_000 }).then((r) => r.data)
}

export function listDatabases(id: number) {
  return apiClient.get<ListResponse<string>>(`/db-instances/${id}/databases`).then((r) => r.data)
}

export function listTables(id: number, database: string) {
  return apiClient
    .get<ListResponse<string>>(`/db-instances/${id}/tables`, { params: { database } })
    .then((r) => r.data)
}

// The server narrows this to the caller's own entries unless they are an
// admin, so there is no "mine" parameter to pass.
export function listQueryLogs(params: { instance_id?: number; limit?: number; offset?: number } = {}) {
  return apiClient.get<ListResponse<DBQueryLog>>('/db-query-logs', { params }).then((r) => r.data)
}

export function getQueryLog(id: number) {
  return apiClient.get<DBQueryLogDetail>(`/db-query-logs/${id}`).then((r) => r.data)
}
