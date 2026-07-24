import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  App,
  Button,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tag,
} from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import * as api from '@/api/applications'
import type {
  ApplicationEnvTarget,
  ApplicationGatewayRoute,
  ApplicationGatewayRouteRequest,
  Environment,
  GatewayBackendMode,
  GatewayInstanceSelectMode,
} from '@/api/types'
import { StatusPill } from '@/components/StatusPill'
import { RefreshButton } from '@/components/RefreshButton'
import { errorMessage } from '@/utils/errorMessage'
import { buildTargetsPayload, routesForEnv, routeToRequest } from './targetPayload'

const DEFAULT_INSTANCE_HEADER = 'X-Agenda-Instance'

interface RouteFormValues {
  env: Environment
  route_key: string
  host?: string
  path_prefix: string
  backend_path?: string
  strip_prefix: boolean
  enabled: boolean
  backend_mode: GatewayBackendMode
  instance_select_mode: GatewayInstanceSelectMode
  instance_header?: string
  backends?: { target_id: number; weight: number }[]
}

// A route rendered in the table, tagged with its env for grouping.
interface RouteRow extends ApplicationGatewayRoute {
  _rowKey: string
}

export function RoutesTab({ appId }: { appId: number }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<ApplicationGatewayRoute | null>(null)
  const [form] = Form.useForm<RouteFormValues>()
  const backendMode = Form.useWatch('backend_mode', form)
  const selectMode = Form.useWatch('instance_select_mode', form)
  const formEnv = Form.useWatch('env', form)

  const { data, isLoading } = useQuery({
    queryKey: ['applications', appId, 'instances'],
    queryFn: () => api.listApplicationInstances(appId),
  })
  const targets = useMemo(() => data?.data ?? [], [data])

  // Envs that have at least one instance — a route needs a backend instance to
  // point at, and the write path only persists routes attached to a target.
  const envsWithInstances = useMemo(() => {
    const set = new Set<Environment>()
    for (const t of targets) set.add(t.env)
    return [...set]
  }, [targets])

  // Deduped routes across all envs (routes are echoed onto every target of an
  // env, so take each env's first target as authoritative).
  const rows = useMemo<RouteRow[]>(() => {
    const out: RouteRow[] = []
    const seen = new Set<Environment>()
    for (const t of targets) {
      if (seen.has(t.env)) continue
      seen.add(t.env)
      for (const r of t.gateway_routes ?? []) {
        out.push({ ...r, _rowKey: `${t.env}:${r.route_key}` })
      }
    }
    return out
  }, [targets])

  const saveMutation = useMutation({
    mutationFn: ({ env, routes }: { env: Environment; routes: ApplicationGatewayRouteRequest[] }) =>
      api.updateApplication(appId, { targets: buildTargetsPayload(targets, [], { env, routes }) }),
    onSuccess: () => {
      message.success('Routes updated.')
      queryClient.invalidateQueries({ queryKey: ['applications', appId] })
      setModalOpen(false)
      setEditing(null)
      form.resetFields()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  function openCreate() {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({
      env: envsWithInstances[0],
      path_prefix: '/',
      strip_prefix: false,
      enabled: true,
      backend_mode: 'all_enabled',
      instance_select_mode: 'disabled',
      instance_header: DEFAULT_INSTANCE_HEADER,
      backends: [],
    })
    setModalOpen(true)
  }

  function openEdit(route: ApplicationGatewayRoute) {
    setEditing(route)
    form.setFieldsValue({
      env: route.env,
      route_key: route.route_key,
      host: route.host,
      path_prefix: route.path_prefix,
      backend_path: route.backend_path,
      strip_prefix: route.strip_prefix,
      enabled: route.enabled,
      backend_mode: route.backend_mode,
      instance_select_mode: route.instance_select_mode,
      instance_header: route.instance_header || DEFAULT_INSTANCE_HEADER,
      backends: (route.backends ?? []).map((b) => ({ target_id: b.target_id, weight: b.weight })),
    })
    setModalOpen(true)
  }

  // Rebuilds the target env's route list with the edited/created route applied,
  // then persists it. Editing keys on the ORIGINAL route_key (so a rename still
  // replaces the right row); creating appends and lets the backend reject a
  // duplicate route_key within the env.
  function submit(values: RouteFormValues) {
    const existing = routesForEnv(targets, values.env).map(routeToRequest)
    const next: ApplicationGatewayRouteRequest = {
      id: editing?.id,
      route_key: values.route_key.trim(),
      host: (values.host ?? '').trim(),
      path_prefix: values.path_prefix.trim() || '/',
      backend_path: (values.backend_path ?? '').trim(),
      strip_prefix: values.strip_prefix,
      enabled: values.enabled,
      backend_mode: values.backend_mode,
      instance_select_mode: values.instance_select_mode,
      instance_header:
        values.instance_select_mode === 'enabled'
          ? (values.instance_header ?? '').trim() || DEFAULT_INSTANCE_HEADER
          : '',
      backends:
        values.backend_mode === 'selected'
          ? (values.backends ?? []).map((b) => ({ target_id: b.target_id, weight: b.weight || 100 }))
          : [],
    }

    const routes = editing
      ? existing.map((r) => (r.route_key === editing.route_key ? next : r))
      : [...existing, next]

    saveMutation.mutate({ env: values.env, routes })
  }

  function removeRoute(route: ApplicationGatewayRoute) {
    const routes = routesForEnv(targets, route.env)
      .filter((r) => r.route_key !== route.route_key)
      .map(routeToRequest)
    saveMutation.mutate({ env: route.env, routes })
  }

  const envInstances = (env: Environment | undefined) =>
    env ? targets.filter((t) => t.env === env) : []

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
        <span style={{ color: 'var(--agenda-ink-500, #888)', fontSize: 13 }}>
          Gateway routes map an inbound host/path to this app's instances. Routes are scoped per environment.
        </span>
        <Space>
          <RefreshButton queryKeys={[['applications', appId, 'instances']]} />
          <Button icon={<PlusOutlined />} onClick={openCreate} disabled={envsWithInstances.length === 0}>
            Add route
          </Button>
        </Space>
      </div>

      <Table<RouteRow>
        rowKey="_rowKey"
        loading={isLoading}
        dataSource={rows}
        pagination={false}
        columns={[
          { title: 'Env', dataIndex: 'env', render: (v) => <Tag>{v}</Tag> },
          { title: 'Route key', dataIndex: 'route_key', render: (v) => <span className="agenda-mono">{v}</span> },
          {
            title: 'Match',
            key: 'match',
            render: (_, r) => (
              <span className="agenda-mono" style={{ fontSize: 12 }}>
                {(r.host || '*') + r.path_prefix}
              </span>
            ),
          },
          {
            title: 'Backends',
            key: 'backends',
            render: (_, r) =>
              r.backend_mode === 'selected'
                ? `selected (${r.backends?.length ?? 0})`
                : r.backend_mode === 'all_enabled'
                  ? 'all enabled'
                  : 'single',
          },
          {
            title: 'Enabled',
            dataIndex: 'enabled',
            render: (v: boolean) =>
              v ? <StatusPill status="verified" label="enabled" /> : <StatusPill status="idle" label="disabled" />,
          },
          {
            title: '',
            key: 'actions',
            render: (_, r) => (
              <Space>
                <Button size="small" onClick={() => openEdit(r)}>
                  Edit
                </Button>
                <Popconfirm title="Delete this route?" onConfirm={() => removeRoute(r)}>
                  <Button size="small" danger>
                    Delete
                  </Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
        locale={{
          emptyText:
            envsWithInstances.length === 0
              ? 'Add an instance first — a route needs a backend instance to point at.'
              : 'No gateway routes yet.',
        }}
      />

      <Modal
        title={editing ? 'Edit route' : 'Add route'}
        open={modalOpen}
        onCancel={() => {
          setModalOpen(false)
          setEditing(null)
        }}
        onOk={() => form.submit()}
        confirmLoading={saveMutation.isPending}
        okText="Save"
        width={560}
      >
        <Form form={form} layout="vertical" requiredMark={false} onFinish={submit}>
          <Form.Item name="env" label="Environment" rules={[{ required: true }]}>
            <Select
              disabled={!!editing}
              options={envsWithInstances.map((e) => ({ value: e, label: e }))}
            />
          </Form.Item>
          <Form.Item
            name="route_key"
            label="Route key"
            rules={[{ required: true, message: 'A stable identifier for this route.' }]}
            extra="Unique per environment, e.g. api or web."
          >
            <Input placeholder="api" />
          </Form.Item>
          <Form.Item
            name="host"
            label="Host"
            extra="Leave empty to match any host (wildcard). If edge TLS is enabled, the gateway auto-issues an HTTPS certificate for this host via ACME DNS-01 — the domain's DNS must be hosted on Aliyun (NS → *.alidns.com) and the first issue can take a few minutes to propagate before HTTPS works."
          >
            <Input placeholder="api.example.com" />
          </Form.Item>
          <Form.Item name="path_prefix" label="Path prefix" rules={[{ required: true }]}>
            <Input placeholder="/" />
          </Form.Item>
          <Form.Item name="strip_prefix" label="Strip prefix before proxying" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="backend_path" label="Backend path" extra="Optional path to prepend when proxying upstream.">
            <Input placeholder="/" />
          </Form.Item>
          <Form.Item name="backend_mode" label="Backend mode" rules={[{ required: true }]}>
            <Select
              options={[
                { value: 'all_enabled', label: 'All enabled instances' },
                { value: 'single', label: 'Single instance' },
                { value: 'selected', label: 'Selected instances (weighted)' },
              ]}
            />
          </Form.Item>
          {backendMode === 'selected' && (
            <Form.List name="backends">
              {(fields, { add, remove }) => (
                <div style={{ marginBottom: 16 }}>
                  {fields.map((field) => (
                    <Space key={field.key} align="baseline" style={{ display: 'flex', marginBottom: 8 }}>
                      <Form.Item
                        name={[field.name, 'target_id']}
                        rules={[{ required: true, message: 'Pick an instance.' }]}
                        noStyle
                      >
                        <Select
                          style={{ width: 260 }}
                          placeholder="Instance"
                          options={envInstances(formEnv).map((t: ApplicationEnvTarget) => ({
                            value: t.id,
                            label: `${t.instance_name} (#${t.id})`,
                          }))}
                        />
                      </Form.Item>
                      <Form.Item name={[field.name, 'weight']} noStyle initialValue={100}>
                        <InputNumber min={1} placeholder="weight" style={{ width: 100 }} />
                      </Form.Item>
                      <Button size="small" onClick={() => remove(field.name)}>
                        Remove
                      </Button>
                    </Space>
                  ))}
                  <Button size="small" onClick={() => add({ weight: 100 })} icon={<PlusOutlined />}>
                    Add backend
                  </Button>
                </div>
              )}
            </Form.List>
          )}
          <Form.Item name="instance_select_mode" label="Instance pinning" rules={[{ required: true }]}>
            <Select
              options={[
                { value: 'disabled', label: 'Disabled' },
                { value: 'enabled', label: 'Enabled (pin by header)' },
              ]}
            />
          </Form.Item>
          {selectMode === 'enabled' && (
            <Form.Item name="instance_header" label="Instance header">
              <Input placeholder={DEFAULT_INSTANCE_HEADER} />
            </Form.Item>
          )}
          <Form.Item name="enabled" label="Enabled" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
