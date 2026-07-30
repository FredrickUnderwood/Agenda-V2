import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { App, Button, Form, Input, Modal, Popconfirm, Select, Switch, Table, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import * as api from '@/api/settings'
import type { Setting, SettingType, UpsertSettingRequest } from '@/api/types'
import { errorMessage } from '@/utils/errorMessage'
import { isManagedKey } from './registry'
import { SETTINGS_QUERY_KEY, useSettings } from './useSettings'

interface FormValues extends UpsertSettingRequest {
  key: string
}

// Raw key/value escape hatch: the Setting store is generic, so this both surfaces
// any key not owned by a purpose-built editor above (nothing is ever invisible)
// and lets an operator add an arbitrary key the UI doesn't model yet.
export function AdvancedSection() {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const { list, isLoading } = useSettings()
  const [modalOpen, setModalOpen] = useState(false)
  const [form] = Form.useForm<FormValues>()

  const rows = list.filter((s) => !isManagedKey(s.key))

  const invalidate = () => queryClient.invalidateQueries({ queryKey: SETTINGS_QUERY_KEY })

  const upsertMutation = useMutation({
    mutationFn: ({ key, ...req }: FormValues) => api.upsertSetting(key.trim(), req),
    onSuccess: () => {
      message.success('Setting saved.')
      invalidate()
      setModalOpen(false)
      form.resetFields()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (key: string) => api.deleteSetting(key),
    onSuccess: () => {
      message.success('Setting removed.')
      invalidate()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 16 }}>
        <div style={{ maxWidth: 560 }}>
          <Typography.Title level={4} className="agenda-display" style={{ marginTop: 0 }}>
            Advanced
          </Typography.Title>
          <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
            Raw key/value settings not covered by the sections above. Only settings the UI doesn’t model appear here — the
            store is a plain key/value table, so this is the escape hatch for one-off keys.
          </Typography.Paragraph>
        </div>
        <Button icon={<PlusOutlined />} onClick={() => setModalOpen(true)}>
          Add raw setting
        </Button>
      </div>

      <Table<Setting>
        rowKey="key"
        loading={isLoading}
        dataSource={rows}
        pagination={false}
        columns={[
          { title: 'Key', dataIndex: 'key', render: (v: string) => <span className="agenda-mono">{v}</span> },
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
            width: 1,
            render: (_, s) => (
              <Popconfirm title={`Remove ${s.key}?`} onConfirm={() => deleteMutation.mutate(s.key)}>
                <Button size="small" danger>
                  Remove
                </Button>
              </Popconfirm>
            ),
          },
        ]}
        locale={{ emptyText: 'No unmodeled settings — everything configured is managed by a section above.' }}
      />

      <Modal
        title="Add raw setting"
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={() => form.submit()}
        confirmLoading={upsertMutation.isPending}
        okText="Save"
      >
        <Form
          form={form}
          layout="vertical"
          requiredMark={false}
          initialValues={{ type: 'string' as SettingType, is_secret: false }}
          onFinish={(v) => upsertMutation.mutate(v)}
        >
          <Form.Item name="key" label="Key" rules={[{ required: true, message: 'Key is required' }]} extra="e.g. feature.new_dashboard">
            <Input className="agenda-mono" />
          </Form.Item>
          <Form.Item name="value" label="Value" rules={[{ required: true, message: 'Value is required' }]}>
            <Input.Password visibilityToggle className="agenda-mono" autoComplete="new-password" />
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
