import { apiClient } from './client'
import type { ListResponse, Notification } from './types'

export function listNotifications(opts?: { unreadOnly?: boolean; limit?: number; offset?: number }) {
  return apiClient
    .get<ListResponse<Notification>>('/notifications', {
      params: { unread_only: opts?.unreadOnly ? 'true' : undefined, limit: opts?.limit, offset: opts?.offset },
    })
    .then((r) => r.data)
}

export function getUnreadNotificationCount() {
  return apiClient.get<{ count: number }>('/notifications/unread-count').then((r) => r.data.count)
}

export function markNotificationRead(id: number) {
  return apiClient.post<{ ok: boolean }>(`/notifications/${id}/read`).then((r) => r.data)
}

export function markAllNotificationsRead() {
  return apiClient.post<{ ok: boolean }>('/notifications/read-all').then((r) => r.data)
}

export function deleteNotification(id: number) {
  return apiClient.delete(`/notifications/${id}`)
}
