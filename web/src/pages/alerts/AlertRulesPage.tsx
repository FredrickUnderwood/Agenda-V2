import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Form, Input, InputNumber, Modal, Popconfirm, Select, Switch, Table, Tag, Tooltip, Typography } from 'antd'
import { PlusOutlined, PlayCircleOutlined } from '@ant-design/icons'
import * as api from '@/api/alertRules'
import * as alertsApi from '@/api/alerts'
import type { AlertRule, UpsertAlertRuleRequest } from '@/api/types'
import { StatusPill } from '@/components/StatusPill'
import { getGrafanaBaseUrl } from '@/utils/grafana'
import { errorMessage } from '@/utils/errorMessage'

export function AlertRulesPage() {
  const { message, modal } = App.useApp()
  const queryClient = useQueryClient()
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<AlertRule | null>(null)
  const [form] = Form.useForm<UpsertAlertRuleRequest>()

  const { data, isLoading } = useQuery({ queryKey: ['alert-rules'], queryFn: api.listAlertRules })
  const { data: channels } = useQuery({ queryKey: ['alert-channels'], queryFn: alertsApi.listAlertChannels })

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['alert-rules'] })

  const createMutation = useMutation({
    mutationFn: (req: UpsertAlertRuleRequest) => api.createAlertRule(req),
    onSuccess: () => {
      message.success('Alert rule created.')
      invalidate()
      closeModal()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, req }: { id: number; req: UpsertAlertRuleRequest }) => api.updateAlertRule(id, req),
    onSuccess: () => {
      message.success('Alert rule saved.')
      invalidate()
      closeModal()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.deleteAlertRule(id),
    onSuccess: () => {
      message.success('Alert rule removed.')
      invalidate()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const testMutation = useMutation({
    mutationFn: (id: number) => api.testAlertRule(id),
    onSuccess: (res) => {
      modal.info({
        title: res.firing ? 'Rule would fire' : 'Rule is OK',
        content: (
          <pre style={{ whiteSpace: 'pre-wrap', maxHeight: 300, overflow: 'auto' }}>
            {res.error ?? JSON.stringify(res.result, null, 2)}
          </pre>
        ),
      })
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  function closeModal() {
    setModalOpen(false)
    setEditing(null)
    form.resetFields()
  }

  function openCreate() {
    setEditing(null)
    form.resetFields()
    setModalOpen(true)
  }

  function openEdit(rule: AlertRule) {
    setEditing(rule)
    form.setFieldsValue({
      name: rule.name,
      expr: rule.expr,
      for_seconds: rule.for_seconds,
      level: rule.level,
      channels: rule.channels,
      enabled: rule.enabled,
    })
    setModalOpen(true)
  }

  function submit(values: UpsertAlertRuleRequest) {
    if (editing) {
      updateMutation.mutate({ id: editing.id, req: values })
    } else {
      createMutation.mutate(values)
    }
  }

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
        <div>
          <Typography.Title level={3} className="agenda-display" style={{ margin: 0 }}>
            Alert rules
          </Typography.Title>
          <Typography.Text type="secondary">
            PromQL conditions evaluated against Prometheus — firing sends to the selected alert channels and the inbox.
          </Typography.Text>
        </div>
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
          Add rule
        </Button>
      </div>

      <Table<AlertRule>
        rowKey="id"
        loading={isLoading}
        dataSource={data?.data ?? []}
        pagination={false}
        columns={[
          { title: 'Name', dataIndex: 'name' },
          {
            title: 'Expression',
            dataIndex: 'expr',
            render: (v: string) => (
              <Tooltip title={v}>
                <span className="agenda-mono" style={{ maxWidth: 280, display: 'inline-block', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', verticalAlign: 'bottom' }}>
                  {v}
                </span>
              </Tooltip>
            ),
          },
          { title: 'State', dataIndex: 'state', render: (v: string) => <StatusPill status={v} /> },
          { title: 'Level', dataIndex: 'level', render: (v: string) => <Tag>{v}</Tag> },
          {
            title: 'Channels',
            dataIndex: 'channels',
            render: (v: string[]) => (v.length ? v.map((c) => <Tag key={c}>{c}</Tag>) : <Typography.Text type="secondary">all enabled</Typography.Text>),
          },
          {
            title: 'Enabled',
            dataIndex: 'enabled',
            render: (v: boolean, rule) => (
              <Switch
                checked={v}
                onChange={(checked) => updateMutation.mutate({ id: rule.id, req: toRequest(rule, { enabled: checked }) })}
              />
            ),
          },
          {
            title: '',
            key: 'actions',
            render: (_, rule) => (
              <div style={{ display: 'flex', gap: 8 }}>
                <Button size="small" icon={<PlayCircleOutlined />} loading={testMutation.isPending && testMutation.variables === rule.id} onClick={() => testMutation.mutate(rule.id)}>
                  Test
                </Button>
                <Button size="small" onClick={() => openEdit(rule)}>
                  Edit
                </Button>
                <Popconfirm title="Remove this alert rule?" onConfirm={() => deleteMutation.mutate(rule.id)}>
                  <Button size="small" danger>
                    Remove
                  </Button>
                </Popconfirm>
              </div>
            ),
          },
        ]}
        locale={{ emptyText: 'No alert rules yet — add one to get paged when a custom metric breaches.' }}
      />

      <Modal
        title={editing ? 'Edit alert rule' : 'Add alert rule'}
        open={modalOpen}
        onCancel={closeModal}
        onOk={() => form.submit()}
        confirmLoading={createMutation.isPending || updateMutation.isPending}
        okText="Save"
        width={560}
      >
        <Form form={form} layout="vertical" requiredMark={false} initialValues={{ for_seconds: 0, level: 'warning', enabled: true }} onFinish={submit}>
          <Form.Item name="name" label="Name" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item
            name="expr"
            label="PromQL expression"
            rules={[{ required: true }]}
            extra={
              <>
                Instant query — a non-empty result vector fires the rule, e.g.{' '}
                <code>rate(orders_failed_total[5m]) &gt; 0.1</code>. Try it in{' '}
                <a href={`${getGrafanaBaseUrl()}/explore`} target="_blank" rel="noreferrer">
                  Grafana Explore
                </a>
                .
              </>
            }
          >
            <Input.TextArea className="agenda-mono" rows={3} placeholder="rate(orders_failed_total[5m]) > 0.1" />
          </Form.Item>
          <Form.Item name="for_seconds" label="For (seconds)" extra="How long the expression must keep breaching before firing. 0 fires on the first breach.">
            <InputNumber min={0} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="level" label="Level" rules={[{ required: true }]}>
            <Select
              options={[
                { value: 'info', label: 'Info' },
                { value: 'warning', label: 'Warning' },
                { value: 'critical', label: 'Critical' },
              ]}
            />
          </Form.Item>
          <Form.Item name="channels" label="Channels" extra="Leave empty to send to every enabled channel.">
            <Select mode="multiple" options={channels?.map((c) => ({ value: c.name, label: `${c.name} (${c.type})` }))} placeholder="All enabled channels" />
          </Form.Item>
          <Form.Item name="enabled" label="Enabled" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

function toRequest(rule: AlertRule, overrides: Partial<UpsertAlertRuleRequest>): UpsertAlertRuleRequest {
  return {
    name: rule.name,
    expr: rule.expr,
    for_seconds: rule.for_seconds,
    level: rule.level,
    channels: rule.channels,
    enabled: rule.enabled,
    ...overrides,
  }
}
