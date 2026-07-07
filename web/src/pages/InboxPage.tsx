import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Empty, Popconfirm, Segmented, Spin, Tag, Typography } from 'antd'
import { DeleteOutlined } from '@ant-design/icons'
import * as api from '@/api/notifications'
import type { NotificationLevel } from '@/api/types'
import { color } from '@/theme/tokens'

const LEVEL_TAG_COLOR: Record<NotificationLevel, string> = {
  critical: 'error',
  warning: 'warning',
  info: 'success',
}

export function InboxPage() {
  const [filter, setFilter] = useState<'all' | 'unread'>('all')
  const queryClient = useQueryClient()

  const { data, isLoading } = useQuery({
    queryKey: ['notifications', 'list', filter],
    queryFn: () => api.listNotifications({ unreadOnly: filter === 'unread', limit: 200 }),
  })

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['notifications'] })
  const markReadMutation = useMutation({ mutationFn: api.markNotificationRead, onSuccess: invalidate })
  const markAllReadMutation = useMutation({ mutationFn: api.markAllNotificationsRead, onSuccess: invalidate })
  const deleteMutation = useMutation({ mutationFn: api.deleteNotification, onSuccess: invalidate })

  const items = data?.data ?? []

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
        <Typography.Title level={3} className="agenda-display" style={{ margin: 0 }}>
          Inbox
        </Typography.Title>
        <div style={{ display: 'flex', gap: 12 }}>
          <Segmented
            value={filter}
            onChange={(v) => setFilter(v as 'all' | 'unread')}
            options={[
              { label: 'All', value: 'all' },
              { label: 'Unread', value: 'unread' },
            ]}
          />
          <Button onClick={() => markAllReadMutation.mutate()} loading={markAllReadMutation.isPending}>
            Mark all read
          </Button>
        </div>
      </div>

      {isLoading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 48 }}>
          <Spin />
        </div>
      ) : items.length === 0 ? (
        <Empty description="No notifications" style={{ marginTop: 48 }} />
      ) : (
        <div style={{ background: color.paperRaised, border: `1px solid ${color.paperBorder}`, borderRadius: 8 }}>
          {items.map((n, i) => (
            <div
              key={n.id}
              onClick={() => !n.is_read && markReadMutation.mutate(n.id)}
              style={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'flex-start',
                gap: 12,
                padding: '14px 16px',
                borderTop: i === 0 ? 'none' : `1px solid ${color.paperBorder}`,
                opacity: n.is_read ? 0.6 : 1,
                cursor: n.is_read ? 'default' : 'pointer',
              }}
            >
              <div style={{ minWidth: 0, flex: 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  {!n.is_read && (
                    <span style={{ width: 6, height: 6, borderRadius: 999, background: color.signal, flex: 'none' }} />
                  )}
                  <span style={{ fontWeight: 500 }}>{n.title}</span>
                  <Tag color={LEVEL_TAG_COLOR[n.level]}>{n.level}</Tag>
                </div>
                {n.content && <div style={{ marginTop: 4, color: color.ink900 }}>{n.content}</div>}
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {new Date(n.created_at).toLocaleString()}
                </Typography.Text>
              </div>
              <Popconfirm title="Delete this notification?" onConfirm={() => deleteMutation.mutate(n.id)}>
                <Button
                  size="small"
                  type="text"
                  danger
                  icon={<DeleteOutlined />}
                  onClick={(e) => e.stopPropagation()}
                  style={{ flex: 'none' }}
                />
              </Popconfirm>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
