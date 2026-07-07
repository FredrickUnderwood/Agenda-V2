import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Form, Input, Modal, Popconfirm, Select, Switch, Table, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import * as api from '@/api/settings'
import type { Setting, UpsertSettingRequest } from '@/api/types'
import { errorMessage } from '@/utils/errorMessage'

interface FormValues extends UpsertSettingRequest {
  key: string
}

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
