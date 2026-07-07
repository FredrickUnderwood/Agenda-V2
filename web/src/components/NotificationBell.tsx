import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Badge, Button, Empty, Popover, Typography } from 'antd'
import { BellOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import * as api from '@/api/notifications'
import type { Notification, NotificationLevel } from '@/api/types'
import { color } from '@/theme/tokens'

const LEVEL_COLOR: Record<NotificationLevel, string> = {
  critical: color.fail,
  warning: color.signal,
  info: color.verified,
}

export function NotificationBell() {
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const { data: unreadCount } = useQuery({
    queryKey: ['notifications', 'unread-count'],
    queryFn: api.getUnreadNotificationCount,
    refetchInterval: 30_000,
  })

  const { data: recent } = useQuery({
    queryKey: ['notifications', 'recent'],
    queryFn: () => api.listNotifications({ limit: 8 }),
    enabled: open,
  })

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['notifications'] })
  }
  const markReadMutation = useMutation({
    mutationFn: (id: number) => api.markNotificationRead(id),
    onSuccess: invalidate,
  })
  const markAllReadMutation = useMutation({
    mutationFn: api.markAllNotificationsRead,
    onSuccess: invalidate,
  })

  return (
    <Popover
      trigger="click"
      open={open}
      onOpenChange={setOpen}
      placement="bottomRight"
      title={
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', minWidth: 320 }}>
          <span>Notifications</span>
          <Button type="link" size="small" onClick={() => markAllReadMutation.mutate()} disabled={!unreadCount}>
            Mark all read
          </Button>
        </div>
      }
      content={
        <div style={{ width: 340, maxHeight: 360, overflow: 'auto' }}>
          {!recent?.data.length ? (
            <Empty description="No notifications yet" style={{ margin: '24px 0' }} />
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4, width: '100%' }}>
              {recent.data.map((n: Notification) => (
                <div
                  key={n.id}
                  onClick={() => !n.is_read && markReadMutation.mutate(n.id)}
                  style={{
                    display: 'flex',
                    gap: 8,
                    padding: '8px 4px',
                    cursor: n.is_read ? 'default' : 'pointer',
                    opacity: n.is_read ? 0.55 : 1,
                    borderBottom: `1px solid ${color.paperBorder}`,
                  }}
                >
                  <span
                    style={{
                      marginTop: 6,
                      width: 6,
                      height: 6,
                      borderRadius: 999,
                      flex: 'none',
                      background: LEVEL_COLOR[n.level] ?? color.ink500,
                    }}
                  />
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontWeight: 500, fontSize: 13 }}>{n.title}</div>
                    {n.content && (
                      <Typography.Text type="secondary" style={{ fontSize: 12 }} ellipsis>
                        {n.content}
                      </Typography.Text>
                    )}
                    <div style={{ fontSize: 11, color: color.ink500 }}>{new Date(n.created_at).toLocaleString()}</div>
                  </div>
                </div>
              ))}
            </div>
          )}
          <Button
            type="link"
            block
            style={{ marginTop: 8 }}
            onClick={() => {
              setOpen(false)
              navigate('/inbox')
            }}
          >
            View all
          </Button>
        </div>
      }
    >
      <Badge count={unreadCount ?? 0} size="small" offset={[-2, 2]}>
        <BellOutlined style={{ fontSize: 18, cursor: 'pointer', color: color.ink900 }} />
      </Badge>
    </Popover>
  )
}
