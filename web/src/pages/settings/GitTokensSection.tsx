import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { App, Button, Form, Input, Modal, Popconfirm, Table, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import * as api from '@/api/settings'
import { errorMessage } from '@/utils/errorMessage'
import { SETTINGS_QUERY_KEY, useSettings } from './useSettings'

const PREFIX = 'git.token.'

interface TokenRow {
  key: string
  host: string
  updated_at: string
}

// Per-host git credentials, stored as secret settings keyed git.token.<host>
// (see internal/service/setting_service.go GitToken). The host is the repo URL's
// hostname — e.g. github.com — resolved on every clone/fetch.
export function GitTokensSection() {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const { list, isLoading } = useSettings()
  const [modalOpen, setModalOpen] = useState(false)
  const [form] = Form.useForm<{ host: string; token: string }>()

  const rows: TokenRow[] = list
    .filter((s) => s.key.startsWith(PREFIX) && s.key.length > PREFIX.length)
    .map((s) => ({ key: s.key, host: s.key.slice(PREFIX.length), updated_at: s.updated_at }))

  const invalidate = () => queryClient.invalidateQueries({ queryKey: SETTINGS_QUERY_KEY })

  const upsertMutation = useMutation({
    mutationFn: ({ host, token }: { host: string; token: string }) =>
      api.upsertSetting(`${PREFIX}${host.trim()}`, { value: token, type: 'string', is_secret: true }),
    onSuccess: () => {
      message.success('Git token saved.')
      invalidate()
      setModalOpen(false)
      form.resetFields()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (key: string) => api.deleteSetting(key),
    onSuccess: () => {
      message.success('Git token removed.')
      invalidate()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 16 }}>
        <div style={{ maxWidth: 560 }}>
          <Typography.Title level={4} className="agenda-display" style={{ marginTop: 0 }}>
            Git tokens
          </Typography.Title>
          <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
            Access tokens for private repositories, one per host. The control plane injects the token for a repo’s host on
            every clone and fetch, and redacts it from deploy logs. Tokens are encrypted at rest and never shown again.
          </Typography.Paragraph>
        </div>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setModalOpen(true)}>
          Add token
        </Button>
      </div>

      <Table<TokenRow>
        rowKey="key"
        loading={isLoading}
        dataSource={rows}
        pagination={false}
        columns={[
          { title: 'Host', dataIndex: 'host', render: (v: string) => <span className="agenda-mono">{v}</span> },
          { title: 'Token', key: 'token', render: () => <span className="agenda-mono">••••••••</span> },
          { title: 'Updated', dataIndex: 'updated_at', render: (v: string) => new Date(v).toLocaleString() },
          {
            title: '',
            key: 'actions',
            width: 1,
            render: (_, r) => (
              <Popconfirm title={`Remove the token for ${r.host}?`} onConfirm={() => deleteMutation.mutate(r.key)}>
                <Button size="small" danger>
                  Remove
                </Button>
              </Popconfirm>
            ),
          },
        ]}
        locale={{ emptyText: 'No git tokens — public repos need none; add one for a private host.' }}
      />

      <Modal
        title="Add git token"
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={() => form.submit()}
        confirmLoading={upsertMutation.isPending}
        okText="Save"
      >
        <Form form={form} layout="vertical" requiredMark={false} onFinish={(v) => upsertMutation.mutate(v)}>
          <Form.Item
            name="host"
            label="Host"
            rules={[{ required: true, message: 'Host is required' }]}
            extra="The repository URL’s hostname, e.g. github.com or gitlab.example.com."
          >
            <Input className="agenda-mono" placeholder="github.com" />
          </Form.Item>
          <Form.Item name="token" label="Token" rules={[{ required: true, message: 'Token is required' }]}>
            <Input.Password className="agenda-mono" autoComplete="new-password" placeholder="ghp_…" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
