import { Avatar, Descriptions, Tag, Typography } from 'antd'
import { useAuth } from '@/auth/AuthContext'
import { color } from '@/theme/tokens'

export function MePage() {
  const { user } = useAuth()
  if (!user) return null

  return (
    <div style={{ maxWidth: 480 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 16, marginBottom: 24 }}>
        <Avatar size={56} style={{ background: color.signal, fontSize: 22 }}>
          {(user.display_name || user.username)[0]?.toUpperCase()}
        </Avatar>
        <div>
          <Typography.Title level={4} className="agenda-display" style={{ margin: 0 }}>
            {user.display_name || user.username}
          </Typography.Title>
          <Tag color={user.role === 'admin' ? 'gold' : 'default'}>{user.role}</Tag>
        </div>
      </div>
      <Descriptions column={1} bordered size="small">
        <Descriptions.Item label="Username">{user.username}</Descriptions.Item>
        <Descriptions.Item label="Role">{user.role}</Descriptions.Item>
        <Descriptions.Item label="Member since">{new Date(user.created_at).toLocaleDateString()}</Descriptions.Item>
      </Descriptions>
    </div>
  )
}
