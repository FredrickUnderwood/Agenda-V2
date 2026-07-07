import { Layout, Menu, Avatar, Dropdown } from 'antd'
import type { MenuProps } from 'antd'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import {
  AppstoreOutlined,
  ClusterOutlined,
  SettingOutlined,
  TeamOutlined,
  UserOutlined,
  LogoutOutlined,
} from '@ant-design/icons'
import { useAuth } from '@/auth/AuthContext'
import { color } from '@/theme/tokens'

const { Sider, Header, Content } = Layout

const NAV_ITEMS: MenuProps['items'] = [
  { key: '/applications', icon: <AppstoreOutlined />, label: 'Applications' },
  { key: '/machines', icon: <ClusterOutlined />, label: 'Machines' },
  { key: '/settings', icon: <SettingOutlined />, label: 'Settings' },
  { key: '/admin/users', icon: <TeamOutlined />, label: 'Users' },
]

function activeKey(pathname: string): string {
  const match = NAV_ITEMS?.find((item) => item && 'key' in item && pathname.startsWith(String(item.key)))
  return match && 'key' in match ? String(match.key) : '/applications'
}

export function AppLayout() {
  const { user, logout } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()

  const userMenu: MenuProps['items'] = [
    { key: 'me', icon: <UserOutlined />, label: 'My account', onClick: () => navigate('/me') },
    { key: 'logout', icon: <LogoutOutlined />, label: 'Sign out', onClick: logout },
  ]

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider width={216} style={{ display: 'flex', flexDirection: 'column' }}>
        <div
          className="agenda-display"
          style={{
            color: '#fff',
            fontSize: 20,
            padding: '20px 24px',
            display: 'flex',
            alignItems: 'center',
            gap: 8,
          }}
        >
          <span style={{ color: color.signal }}>●</span> agenda
        </div>
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={[activeKey(location.pathname)]}
          items={NAV_ITEMS}
          onClick={({ key }) => navigate(key)}
          style={{ flex: 1, borderInlineEnd: 'none' }}
        />
      </Sider>
      <Layout>
        <Header
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'flex-end',
            padding: '0 24px',
            borderBottom: `1px solid ${color.paperBorder}`,
          }}
        >
          <Dropdown menu={{ items: userMenu }} placement="bottomRight">
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}>
              <Avatar size="small" style={{ background: color.signal }}>
                {user?.display_name?.[0]?.toUpperCase() ?? user?.username?.[0]?.toUpperCase() ?? '?'}
              </Avatar>
              <span>{user?.display_name || user?.username}</span>
            </div>
          </Dropdown>
        </Header>
        <Content style={{ padding: 24 }}>
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  )
}
