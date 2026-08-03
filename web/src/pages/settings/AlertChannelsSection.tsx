import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Form, Input, Modal, Popconfirm, Select, Switch, Table, Tag, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import * as api from '@/api/settings'
import * as alertsApi from '@/api/alerts'
import type { AlertChannel } from '@/api/types'
import { errorMessage } from '@/utils/errorMessage'
import { SETTINGS_QUERY_KEY } from './useSettings'

const PREFIX = 'alert.channel.'

const CHANNEL_TYPES = [
  { value: 'feishu', label: 'Feishu' },
  { value: 'dingtalk', label: 'DingTalk' },
  { value: 'wecom', label: 'WeCom' },
  { value: 'slack', label: 'Slack' },
  { value: 'custom', label: 'Custom webhook' },
]

interface ChannelForm {
  type: string
  name: string
  webhook_url: string
  secret?: string
  enabled: boolean
}

function keyFor(type: string, name: string) {
  return `${PREFIX}${type}.${name.trim()}`
}

// Alert channels are stored as one encrypted JSON blob per channel, keyed
// alert.channel.<type>.<name> (see internal/service/alert_service.go). Because the
// value is secret, the webhook URL/secret can't be read back — the list endpoint
// only exposes name/type/enabled — so editing requires re-entering the webhook.
export function AlertChannelsSection() {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  // The channel list comes from the dedicated endpoint that reports each
  // channel's name/type/enabled without exposing the encrypted webhook/secret.
  const { data: channels, isLoading } = useQuery({ queryKey: ['alert-channels'], queryFn: alertsApi.listAlertChannels })

  const [modalOpen, setModalOpen] = useState(false)
  const [editingKey, setEditingKey] = useState<string | null>(null)
  const [form] = Form.useForm<ChannelForm>()

  function invalidate() {
    queryClient.invalidateQueries({ queryKey: SETTINGS_QUERY_KEY })
    queryClient.invalidateQueries({ queryKey: ['alert-channels'] })
  }

  const saveMutation = useMutation({
    mutationFn: (v: ChannelForm) => {
      const value = JSON.stringify({ webhook_url: v.webhook_url.trim(), secret: v.secret?.trim() || undefined, enabled: v.enabled })
      return api.upsertSetting(keyFor(v.type, v.name), { value, type: 'json', is_secret: true })
    },
    onSuccess: () => {
      message.success('Alert channel saved.')
      invalidate()
      closeModal()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (ch: AlertChannel) => api.deleteSetting(keyFor(ch.type, ch.name)),
    onSuccess: () => {
      message.success('Alert channel removed.')
      invalidate()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const testMutation = useMutation({
    mutationFn: (req: alertsApi.TestAlertRequest) => alertsApi.testAlert(req),
    onSuccess: (res) => {
      if (res.ok) message.success('Test alert delivered.')
      else message.error(`Test failed: ${res.error ?? 'unknown error'}`)
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  function openCreate() {
    setEditingKey(null)
    form.resetFields()
    form.setFieldsValue({ type: 'feishu', enabled: true })
    setModalOpen(true)
  }

  function openEdit(ch: AlertChannel) {
    setEditingKey(keyFor(ch.type, ch.name))
    form.resetFields()
    form.setFieldsValue({ type: ch.type, name: ch.name, enabled: ch.enabled, webhook_url: '', secret: '' })
    setModalOpen(true)
  }

  function closeModal() {
    setModalOpen(false)
    setEditingKey(null)
    form.resetFields()
  }

  async function runTest() {
    try {
      const v = await form.validateFields(['type', 'webhook_url'])
      testMutation.mutate({ type: v.type, webhook_url: v.webhook_url, secret: form.getFieldValue('secret') })
    } catch {
      /* validation errors are shown inline */
    }
  }

  const isEditing = editingKey !== null

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 16 }}>
        <div style={{ maxWidth: 560 }}>
          <Typography.Title level={4} className="agenda-display" style={{ marginTop: 0 }}>
            Alert channels
          </Typography.Title>
          <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
            Webhooks that alert rules and in-app alerts fan out to. Credentials are encrypted at rest and never shown
            again — editing a channel requires re-entering its webhook URL.
          </Typography.Paragraph>
        </div>
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
          Add channel
        </Button>
      </div>

      <Table<AlertChannel>
        rowKey={(ch) => keyFor(ch.type, ch.name)}
        loading={isLoading}
        dataSource={channels ?? []}
        pagination={false}
        columns={[
          { title: 'Name', dataIndex: 'name', render: (v: string) => <span className="agenda-mono">{v}</span> },
          { title: 'Type', dataIndex: 'type', render: (v: string) => <Tag>{v}</Tag> },
          {
            title: 'Enabled',
            dataIndex: 'enabled',
            render: (v: boolean) => (v ? <Tag color="green">enabled</Tag> : <Tag>disabled</Tag>),
          },
          {
            title: '',
            key: 'actions',
            width: 1,
            render: (_, ch) => (
              <div style={{ display: 'flex', gap: 8 }}>
                <Button size="small" onClick={() => openEdit(ch)}>
                  Edit
                </Button>
                <Popconfirm title={`Remove channel ${ch.name}?`} onConfirm={() => deleteMutation.mutate(ch)}>
                  <Button size="small" danger>
                    Remove
                  </Button>
                </Popconfirm>
              </div>
            ),
          },
        ]}
        locale={{ emptyText: 'No alert channels — add one so alert rules have somewhere to page.' }}
      />

      <Modal
        title={isEditing ? 'Edit alert channel' : 'Add alert channel'}
        open={modalOpen}
        onCancel={closeModal}
        width={520}
        footer={[
          <Button key="test" onClick={runTest} loading={testMutation.isPending}>
            Send test
          </Button>,
          <Button key="cancel" onClick={closeModal}>
            Cancel
          </Button>,
          <Button key="save" type="primary" loading={saveMutation.isPending} onClick={() => form.submit()}>
            Save
          </Button>,
        ]}
      >
        <Form form={form} layout="vertical" requiredMark={false} onFinish={(v) => saveMutation.mutate(v)}>
          <Form.Item name="type" label="Type" rules={[{ required: true }]}>
            <Select options={CHANNEL_TYPES} disabled={isEditing} />
          </Form.Item>
          <Form.Item
            name="name"
            label="Name"
            rules={[{ required: true, message: 'Name is required' }]}
            extra="A unique label — this is how alert rules reference the channel."
          >
            <Input className="agenda-mono" placeholder="ops-alerts" disabled={isEditing} />
          </Form.Item>
          <Form.Item
            name="webhook_url"
            label="Webhook URL"
            rules={[{ required: true, message: 'Webhook URL is required' }]}
            extra={isEditing ? 'Not shown for security — re-enter to save changes.' : undefined}
          >
            <Input className="agenda-mono" placeholder="https://open.feishu.cn/open-apis/bot/v2/hook/…" />
          </Form.Item>
          <Form.Item name="secret" label="Signing secret" extra="Optional — e.g. a DingTalk / Feishu signing secret.">
            <Input.Password className="agenda-mono" autoComplete="new-password" />
          </Form.Item>
          <Form.Item name="enabled" label="Enabled" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
