import { App as AntApp } from 'antd'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider } from '@/auth/AuthContext'
import { PrivateRoute } from '@/auth/PrivateRoute'
import { AppLayout } from '@/components/AppLayout'
import { LoginPage } from '@/pages/LoginPage'
import { MePage } from '@/pages/MePage'
import { NotFoundPage } from '@/pages/NotFoundPage'
import { UsersPage } from '@/pages/admin/UsersPage'
import { AlertRulesPage } from '@/pages/alerts/AlertRulesPage'
import { ApplicationDetailPage } from '@/pages/applications/ApplicationDetailPage'
import { ApplicationsListPage } from '@/pages/applications/ApplicationsListPage'
import { InstanceLogsPage } from '@/pages/applications/InstanceLogsPage'
import { InboxPage } from '@/pages/InboxPage'
import { MachinesListPage } from '@/pages/machines/MachinesListPage'
import { GatewayPage } from '@/pages/gateway/GatewayPage'
import { SettingsPage } from '@/pages/settings/SettingsPage'

function App() {
  return (
    <AntApp>
      <BrowserRouter>
        <AuthProvider>
          <Routes>
            <Route path="/login" element={<LoginPage />} />
            <Route element={<PrivateRoute />}>
              <Route element={<AppLayout />}>
                <Route index element={<Navigate to="/applications" replace />} />
                <Route path="applications" element={<ApplicationsListPage />} />
                <Route path="applications/:appId" element={<ApplicationDetailPage />} />
                <Route path="applications/:appId/instances/:targetId/logs" element={<InstanceLogsPage />} />
                <Route path="machines" element={<MachinesListPage />} />
                <Route path="gateway" element={<GatewayPage />} />
                <Route path="alert-rules" element={<AlertRulesPage />} />
                <Route path="inbox" element={<InboxPage />} />
                <Route path="settings" element={<SettingsPage />} />
                <Route path="admin/users" element={<UsersPage />} />
                <Route path="me" element={<MePage />} />
                <Route path="*" element={<NotFoundPage />} />
              </Route>
            </Route>
          </Routes>
        </AuthProvider>
      </BrowserRouter>
    </AntApp>
  )
}

export default App
