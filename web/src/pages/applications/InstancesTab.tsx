import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Form, Input, InputNumber, Modal, Select, Switch, Table, Tag } from 'antd'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import { Link } from 'react-router-dom'
import * as api from '@/api/applications'
import * as machinesApi from '@/api/machines'
import type { ApplicationEnvTarget, ApplicationEnvTargetRequest } from '@/api/types'
import { StatusPill } from '@/components/StatusPill'
import { RefreshButton } from '@/components/RefreshButton'
import { errorMessage } from '@/utils/errorMessage'
import { buildTargetsPayload } from './targetPayload'

export function InstancesTab({ appId }: { appId: number }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [modalOpen, setModalOpen] = useState(false)
  const [form] = Form.useForm<ApplicationEnvTargetRequest>()

  const { data, isLoading } = useQuery({
    queryKey: ['applications', appId, 'instances'],
    queryFn: () => api.listApplicationInstances(appId),
  })
  const { data: machines } = useQuery({ queryKey: ['machines'], queryFn: machinesApi.listMachines })
  const metricsEnabled = Form.useWatch('metrics_enabled', form)

  const createMutation = useMutation({
    mutationFn: (req: ApplicationEnvTargetRequest) =>
      api.updateApplication(appId, { targets: buildTargetsPayload(data?.data ?? [], [req]) }),
    onSuccess: () => {
      message.success('Instance created.')
      queryClient.invalidateQueries({ queryKey: ['applications', appId] })
      setModalOpen(false)
      form.resetFields()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const checkHealthMutation = useMutation({
    mutationFn: (targetId: number) => api.checkInstanceHealth(appId, targetId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['applications', appId, 'instances'] }),
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginBottom: 12 }}>
        <RefreshButton queryKeys={[['applications', appId, 'instances'], ['machines']]} />
        <Button icon={<PlusOutlined />} onClick={() => setModalOpen(true)}>
          Add instance
        </Button>
      </div>

      <Table<ApplicationEnvTarget>
        rowKey="id"
        loading={isLoading}
        dataSource={data?.data ?? []}
        pagination={false}
        columns={[
          { title: 'Env', dataIndex: 'env', render: (v) => <Tag>{v}</Tag> },
          { title: 'Instance', dataIndex: 'instance_name', render: (v) => <span className="agenda-mono">{v}</span> },
          {
            title: 'Machine',
            dataIndex: 'machine_id',
            render: (id: number) => machines?.data.find((m) => m.id === id)?.name ?? `#${id}`,
          },
          { title: 'Port', dataIndex: 'port' },
          {
            title: 'Enabled',
            dataIndex: 'enabled',
            render: (v: boolean) => (v ? <StatusPill status="verified" label="enabled" /> : <StatusPill status="idle" label="disabled" />),
          },
          {
            title: 'Health',
            key: 'health',
            render: (_, record) =>
              record.health ? <StatusPill status={record.health.status} /> : <StatusPill status="unknown" label="unchecked" />,
          },
          {
            title: 'Logs',
            key: 'logs',
            render: (_, record) => <Link to={`/applications/${appId}/instances/${record.id}/logs`}>View logs</Link>,
          },
          {
            title: 'Metrics',
            key: 'metrics',
            render: (_, record) =>
              record.metrics_enabled ? (
                <StatusPill status="verified" label={`:${record.metrics_port}`} />
              ) : (
                <StatusPill status="idle" label="disabled" />
              ),
          },
          {
            title: '',
            key: 'actions',
            render: (_, record) => (
              <Button
                size="small"
                icon={<ReloadOutlined />}
                loading={checkHealthMutation.isPending && checkHealthMutation.variables === record.id}
                onClick={() => checkHealthMutation.mutate(record.id)}
              >
                Check health
              </Button>
            ),
          },
        ]}
        locale={{ emptyText: 'No instances yet — add one to deploy this app to a machine.' }}
      />

      <Modal
        title="Add instance"
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={() => form.submit()}
        confirmLoading={createMutation.isPending}
        okText="Add"
      >
        <Form
          form={form}
          layout="vertical"
          requiredMark={false}
          initialValues={{
            env: 'prod',
            instance_name: 'default',
            enabled: true,
            health_check_enabled: false,
            health_check_path: '/healthz',
            metrics_enabled: false,
            metrics_port: 9464,
          }}
          onFinish={(v) => createMutation.mutate(v)}
        >
          <Form.Item name="env" label="Environment" rules={[{ required: true }]}>
            <Select
              options={[
                { value: 'prod', label: 'Production' },
                { value: 'stage', label: 'Staging' },
                { value: 'test', label: 'Test' },
              ]}
            />
          </Form.Item>
          <Form.Item name="instance_name" label="Instance name" rules={[{ required: true }]} extra="e.g. default, blue, green">
            <Input />
          </Form.Item>
          <Form.Item name="machine_id" label="Machine" rules={[{ required: true }]}>
            <Select options={machines?.data.map((m) => ({ value: m.id, label: m.name }))} />
          </Form.Item>
          <Form.Item name="port" label="Port" rules={[{ required: true }]}>
            <InputNumber min={1} max={65535} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="health_check_enabled" label="Health check" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="health_check_path" label="Health check path">
            <Input placeholder="/healthz" />
          </Form.Item>
          <Form.Item
            name="metrics_enabled"
            label="Custom metrics"
            valuePropName="checked"
            extra="Requires an agent-mode machine — Prometheus reaches this instance only through agenda-node's authenticated relay, same as log reading."
          >
            <Switch />
          </Form.Item>
          {metricsEnabled && (
            <Form.Item
              name="metrics_port"
              label="Metrics port"
              rules={[{ required: true }]}
              extra="Host port your app's own compose file publishes as ${APP_METRICS_PORT}; sdk/go/metric listens on it inside the container."
            >
              <InputNumber min={1} max={65535} style={{ width: '100%' }} />
            </Form.Item>
          )}
        </Form>
      </Modal>
    </div>
  )
}
