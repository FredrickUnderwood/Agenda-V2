import { useState } from 'react'
import { Navigate } from 'react-router-dom'
import { App, Button, Form, Input, Typography } from 'antd'
import { useAuth } from '@/auth/AuthContext'
import { getToken } from '@/auth/tokenStore'
import { PipelineRail } from '@/components/PipelineRail'
import { color, font } from '@/theme/tokens'
import { errorMessage } from '@/utils/errorMessage'

export function LoginPage() {
  const { message } = App.useApp()
  const { login } = useAuth()
  const [submitting, setSubmitting] = useState(false)

  if (getToken()) return <Navigate to="/applications" replace />

  async function onFinish(values: { username: string; password: string }) {
    setSubmitting(true)
    try {
      await login(values.username, values.password)
    } catch (err) {
      const status = (err as { response?: { status?: number } }).response?.status
      message.error(status === 401 ? 'Wrong username or password.' : errorMessage(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: color.paper,
      }}
    >
      <div style={{ width: 360 }}>
        <div style={{ textAlign: 'center', marginBottom: 32 }}>
          <div className="agenda-display" style={{ fontSize: 28, color: color.ink900, marginBottom: 8 }}>
            <span style={{ color: color.signal }}>●</span> agenda
          </div>
          <Typography.Text style={{ color: color.ink500, fontFamily: font.body }}>
            Point at a repo. Watch it go live.
          </Typography.Text>
        </div>

        <div
          style={{
            background: color.paperRaised,
            border: `1px solid ${color.paperBorder}`,
            borderRadius: 10,
            padding: '28px 28px 8px',
          }}
        >
          <Form layout="vertical" onFinish={onFinish} requiredMark={false}>
            <Form.Item name="username" label="Username" rules={[{ required: true }]}>
              <Input autoFocus autoComplete="username" size="large" />
            </Form.Item>
            <Form.Item name="password" label="Password" rules={[{ required: true }]}>
              <Input.Password autoComplete="current-password" size="large" />
            </Form.Item>
            <Form.Item>
              <Button type="primary" htmlType="submit" block size="large" loading={submitting}>
                Sign in
              </Button>
            </Form.Item>
          </Form>
        </div>

        <div style={{ marginTop: 28, padding: '0 8px' }} aria-hidden>
          <PipelineRail
            steps={[
              { key: 'pull', label: 'pull', status: 'success' },
              { key: 'build', label: 'build', status: 'success' },
              { key: 'up', label: 'up', status: 'success' },
              { key: 'verify', label: 'verify', status: 'running' },
            ]}
          />
        </div>
      </div>
    </div>
  )
}
