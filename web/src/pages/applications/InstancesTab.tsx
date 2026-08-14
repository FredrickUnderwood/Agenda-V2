import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Form, Input, InputNumber, Modal, Select, Space, Switch, Table, Tag, Typography } from 'antd'
import { PlusOutlined, ReloadOutlined, PoweroffOutlined, DeleteOutlined } from '@ant-design/icons'
import { Link } from 'react-router-dom'
import * as api from '@/api/applications'
import * as machinesApi from '@/api/machines'
import type { ApplicationEnvTarget, ApplicationEnvTargetRequest } from '@/api/types'
import { StatusPill } from '@/components/StatusPill'
import { RefreshButton } from '@/components/RefreshButton'
import { errorMessage } from '@/utils/errorMessage'
import { buildTargetsPayload } from './targetPayload'

// routesLosingLastBackend returns the route keys that would be left with no
// serving backend if `target` were decommissioned — i.e. routes where this
// instance is the only enabled, running backend. Used to warn the operator that
// decommissioning will take those routes offline (502).
//
// It reasons off the same app+env route list the backend attaches to every
// target: a single-mode route always resolves to just the instance it is shown
// on, so it is always at risk; an all_enabled/selected route is at risk only
// when every other participating instance is disabled or already stopped.
function routesLosingLastBackend(
  target: ApplicationEnvTarget,
  siblings: ApplicationEnvTarget[],
): string[] {
  const routes = target.gateway_routes ?? []
  const others = siblings.filter(
    (s) => s.id !== target.id && s.env === target.env && s.enabled && s.desired_state !== 'stopped',
  )
  const atRisk: string[] = []
  for (const route of routes) {
    if (!route.enabled) continue
    if (route.backend_mode === 'single') {
      atRisk.push(route.route_key)
      continue
    }
    if (route.backend_mode === 'selected') {
      const selectedIds = new Set((route.backends ?? []).filter((b) => b.enabled).map((b) => b.target_id))
      const survivor = others.some((o) => selectedIds.has(o.id))
      if (!survivor) atRisk.push(route.route_key)
      continue
    }
    // all_enabled: any other enabled+running instance keeps it serving.
    if (others.length === 0) atRisk.push(route.route_key)
  }
  return atRisk
}

export function InstancesTab({ appId }: { appId: number }) {
  const { message, modal } = App.useApp()
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

  const decommissionMutation = useMutation({
    mutationFn: (targetId: number) => api.decommissionInstance(appId, targetId),
    onSuccess: () => {
      message.success('Instance is being decommissioned — traffic drained, containers tearing down.')
      queryClient.invalidateQueries({ queryKey: ['applications', appId, 'instances'] })
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (targetId: number) => api.deleteInstance(appId, targetId),
    onSuccess: () => {
      message.success('Instance deleted.')
      queryClient.invalidateQueries({ queryKey: ['applications', appId] })
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const instances = data?.data ?? []

  const confirmDecommission = (record: ApplicationEnvTarget) => {
    const atRisk = routesLosingLastBackend(record, instances)
    modal.confirm({
      title: `Decommission ${record.env}/${record.instance_name}?`,
      okText: 'Decommission',
      okButtonProps: { danger: true },
      width: 520,
      content: (
        <div>
          <Typography.Paragraph style={{ marginBottom: atRisk.length ? 12 : 0 }}>
            This drains the instance from the gateway and tears its containers down. The instance
            record and its logs are kept — you can bring it back later by deploying it again. Named
            data volumes (databases, etc.) are preserved.
          </Typography.Paragraph>
          {atRisk.length > 0 && (
            <Typography.Paragraph type="danger" style={{ marginBottom: 0 }}>
              ⚠ This instance is the only serving backend for {atRisk.length === 1 ? 'route' : 'routes'}{' '}
              <span className="agenda-mono">{atRisk.join(', ')}</span> — decommissioning will take{' '}
              {atRisk.length === 1 ? 'it' : 'them'} offline (502) until another instance is deployed.
            </Typography.Paragraph>
          )}
        </div>
      ),
      onOk: () => decommissionMutation.mutateAsync(record.id),
    })
  }

  const confirmDelete = (record: ApplicationEnvTarget) => {
    const lastInEnv = instances.filter((t) => t.env === record.env).length === 1
    modal.confirm({
      title: `Delete ${record.env}/${record.instance_name}?`,
      okText: 'Delete',
      okButtonProps: { danger: true },
      width: 520,
      content: (
        <div>
          <Typography.Paragraph style={{ marginBottom: 12 }}>
            This permanently removes the instance record. Deploy logs and releases are kept as
            history. This cannot be undone — recreating the instance starts it from scratch.
          </Typography.Paragraph>
          <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginBottom: lastInEnv ? 12 : 0 }}>
            Containers and volumes were already removed when the instance was decommissioned. If
            that teardown never finished (the machine was offline), deleting the record abandons the
            retry — remove those containers on the machine by hand.
          </Typography.Paragraph>
          {lastInEnv && (
            <Typography.Paragraph type="danger" style={{ marginBottom: 0 }}>
              ⚠ This is the last instance in <span className="agenda-mono">{record.env}</span> — that
              environment's gateway routes will be disabled (their host/path config is kept).
            </Typography.Paragraph>
          )}
        </div>
      ),
      onOk: () => deleteMutation.mutateAsync(record.id),
    })
  }

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
        dataSource={instances}
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
            title: 'State',
            key: 'desired_state',
            render: (_, record) =>
              record.desired_state === 'stopped' ? (
                <StatusPill status="idle" label="stopped" />
              ) : (
                <StatusPill status="verified" label="running" />
              ),
          },
          {
            title: 'Health',
            key: 'health',
            render: (_, record) =>
              // A stopped instance isn't probed, so its health is meaningless —
              // show it as stopped rather than a stale green or a misleading
              // "unchecked" that reads like a config gap.
              record.desired_state === 'stopped' ? (
                <StatusPill status="idle" label="stopped" />
              ) : record.health ? (
                <StatusPill status={record.health.status} />
              ) : (
                <StatusPill status="unknown" label="unchecked" />
              ),
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
            render: (_, record) =>
              // A stopped instance has no containers to health-check, and it is
              // brought back by deploying it (there is no recommission) — so its
              // affordances are a hint pointing at the Deploy flow, plus Delete,
              // which is only offered here: deleting a running instance would
              // orphan its containers and its gateway backends.
              record.desired_state === 'stopped' ? (
                <Space size="small">
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    Stopped · deploy to restart
                  </Typography.Text>
                  <Button
                    size="small"
                    danger
                    icon={<DeleteOutlined />}
                    loading={deleteMutation.isPending && deleteMutation.variables === record.id}
                    onClick={() => confirmDelete(record)}
                  >
                    Delete
                  </Button>
                </Space>
              ) : (
                <Space size="small">
                  <Button
                    size="small"
                    icon={<ReloadOutlined />}
                    loading={checkHealthMutation.isPending && checkHealthMutation.variables === record.id}
                    onClick={() => checkHealthMutation.mutate(record.id)}
                  >
                    Check health
                  </Button>
                  <Button
                    size="small"
                    danger
                    icon={<PoweroffOutlined />}
                    loading={decommissionMutation.isPending && decommissionMutation.variables === record.id}
                    onClick={() => confirmDecommission(record)}
                  >
                    Decommission
                  </Button>
                </Space>
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
