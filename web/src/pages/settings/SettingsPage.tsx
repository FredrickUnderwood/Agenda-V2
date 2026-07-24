import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Collapse, Form, Input, Modal, Popconfirm, Select, Switch, Table, Tag, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import * as api from '@/api/settings'
import type { Setting, UpsertSettingRequest } from '@/api/types'
import { errorMessage } from '@/utils/errorMessage'

interface FormValues extends UpsertSettingRequest {
  key: string
}

// Well-known Setting keys the gateway's edge-TLS (embedded Caddy) consumes. The
// control plane reads these and pushes them to the gateway, which auto-issues
// HTTPS certs via ACME DNS-01. Secret keys are encrypted at rest. Keep in sync
// with internal/service/gateway_tls_sync_service.go.
const GATEWAY_TLS_KEYS: { key: string; secret: boolean; required?: boolean; hint: string }[] = [
  { key: 'gateway.tls.acme_email', secret: false, required: true, hint: 'ACME account email' },
  { key: 'gateway.tls.aliyun_ak_id', secret: true, required: true, hint: 'Aliyun RAM AccessKey ID (needs AliyunDNSFullAccess)' },
  { key: 'gateway.tls.aliyun_ak_secret', secret: true, required: true, hint: 'Aliyun RAM AccessKey Secret' },
  { key: 'gateway.tls.eab_kid', secret: true, hint: 'ZeroSSL EAB key id (required for ZeroSSL CA)' },
  { key: 'gateway.tls.eab_hmac', secret: true, hint: 'ZeroSSL EAB hmac key' },
  { key: 'gateway.tls.acme_ca', secret: false, hint: 'ACME directory URL — defaults to ZeroSSL DV90' },
  { key: 'gateway.tls.dns_provider', secret: false, hint: 'DNS-01 provider — defaults to alidns' },
  { key: 'gateway.tls.static_domains', secret: false, hint: 'Extra domains to certify beyond route hosts (space/comma separated)' },
]

export function SettingsPage() {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [modalOpen, setModalOpen] = useState(false)
  const [form] = Form.useForm<FormValues>()

  const { data, isLoading } = useQuery({ queryKey: ['settings'], queryFn: api.listSettings })

  const upsertMutation = useMutation({
    mutationFn: ({ key, ...req }: FormValues) => api.upsertSetting(key, req),
    onSuccess: () => {
      message.success('Setting saved.')
      queryClient.invalidateQueries({ queryKey: ['settings'] })
      setModalOpen(false)
      form.resetFields()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (key: string) => api.deleteSetting(key),
    onSuccess: () => {
      message.success('Setting removed.')
      queryClient.invalidateQueries({ queryKey: ['settings'] })
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  // Prefill the add-setting modal for a well-known key. The timeout lets the
  // Modal mount its Form before we set fields (Form instance isn't connected
  // until the Modal opens).
  const openForKey = (key: string, isSecret: boolean) => {
    setModalOpen(true)
    setTimeout(() => form.setFieldsValue({ key, is_secret: isSecret, type: 'string', value: '' }), 0)
  }

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
        <div>
          <Typography.Title level={3} className="agenda-display" style={{ margin: 0 }}>
            Settings
          </Typography.Title>
          <Typography.Text type="secondary">Runtime config — git tokens, feature flags. Changes apply immediately, no restart.</Typography.Text>
        </div>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setModalOpen(true)}>
          Add setting
        </Button>
      </div>

      <Collapse
        style={{ marginBottom: 20 }}
        items={[
          {
            key: 'gateway-tls',
            label: 'Gateway edge TLS — HTTPS 证书签发（ACME DNS-01）',
            children: (
              <div>
                <Typography.Paragraph type="secondary" style={{ marginBottom: 12 }}>
                  When the gateway runs as the TLS edge (GATEWAY_TLS_ENABLED), it auto-issues HTTPS
                  certificates for every route host via ACME DNS-01. Configure the credentials below; the
                  control plane pushes them to the gateway (secrets are encrypted at rest, never stored on
                  the gateway). <b>Prerequisites:</b> each domain's DNS must be hosted on Aliyun (NS →
                  *.alidns.com), and the RAM key needs <span className="agenda-mono">AliyunDNSFullAccess</span>.
                  First issue of a new domain can take a few minutes to propagate.
                </Typography.Paragraph>
                <Table
                  size="small"
                  rowKey="key"
                  pagination={false}
                  dataSource={GATEWAY_TLS_KEYS}
                  columns={[
                    {
                      title: 'Key',
                      dataIndex: 'key',
                      render: (k: string, r) => (
                        <span className="agenda-mono">
                          {k} {r.required ? <Tag color="red">required</Tag> : null}
                          {r.secret ? <Tag color="gold">secret</Tag> : null}
                        </span>
                      ),
                    },
                    { title: 'Description', dataIndex: 'hint' },
                    {
                      title: '',
                      key: 'set',
                      render: (_, r) => (
                        <Button size="small" onClick={() => openForKey(r.key, r.secret)}>
                          Set
                        </Button>
                      ),
                    },
                  ]}
                />
              </div>
            ),
          },
        ]}
      />

      <Table<Setting>
        rowKey="key"
        loading={isLoading}
        dataSource={data?.data ?? []}
        pagination={false}
        columns={[
          { title: 'Key', dataIndex: 'key', render: (v) => <span className="agenda-mono">{v}</span> },
          {
            title: 'Value',
            dataIndex: 'value',
            render: (v: string, s) => <span className="agenda-mono">{s.is_secret ? '••••••••' : v}</span>,
          },
          { title: 'Type', dataIndex: 'type' },
          { title: 'Updated', dataIndex: 'updated_at', render: (v: string) => new Date(v).toLocaleString() },
          {
            title: '',
            key: 'actions',
            render: (_, s) => (
              <Popconfirm title="Remove this setting?" onConfirm={() => deleteMutation.mutate(s.key)}>
                <Button size="small" danger>
                  Remove
                </Button>
              </Popconfirm>
            ),
          },
        ]}
        locale={{ emptyText: 'No settings configured — bootstrap config stays in agenda-v2.yaml until you add one here.' }}
      />

      <Modal
        title="Add setting"
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={() => form.submit()}
        confirmLoading={upsertMutation.isPending}
        okText="Save"
      >
        <Form form={form} layout="vertical" requiredMark={false} initialValues={{ type: 'string', is_secret: false }} onFinish={(v) => upsertMutation.mutate(v)}>
          <Form.Item name="key" label="Key" rules={[{ required: true }]} extra="e.g. git.token.github.com">
            <Input className="agenda-mono" />
          </Form.Item>
          <Form.Item name="value" label="Value" rules={[{ required: true }]}>
            <Input.Password visibilityToggle className="agenda-mono" />
          </Form.Item>
          <Form.Item name="type" label="Type">
            <Select
              options={[
                { value: 'string', label: 'string' },
                { value: 'int', label: 'int' },
                { value: 'bool', label: 'bool' },
                { value: 'json', label: 'json' },
              ]}
            />
          </Form.Item>
          <Form.Item name="is_secret" label="Secret (encrypted at rest)" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
