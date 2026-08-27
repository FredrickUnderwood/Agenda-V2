import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, App, Button, Form, Input, InputNumber, Modal, Popconfirm, Select, Space, Switch, Table, Tag, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import * as api from '@/api/databases'
import * as machineApi from '@/api/machines'
import type { CreateDatabaseInstanceRequest, DatabaseInstance } from '@/api/types'
import { useAuth } from '@/auth/AuthContext'
import { errorMessage } from '@/utils/errorMessage'

const ENV_COLOR: Record<string, string> = { prod: 'red', stage: 'orange', test: 'blue' }

// Each engine has one conventional port, so picking the engine fills it in.
const DEFAULT_PORT: Record<string, number> = { mysql: 3306, redis: 6379 }

// The password field means something slightly different per engine, and on an
// edit it means "leave it alone" for both.
function passwordHint(editing: boolean, isRedis: boolean) {
  if (editing) {
    return 'Leave blank to keep the stored password.'
  }
  if (isRedis) {
    return 'Leave blank if the server has no requirepass. Use an ACL user limited to reads (+@read) where there is one — that is what actually keeps this read-only.'
  }
  return 'Use an account with SELECT and nothing else — that grant is what actually keeps this read-only.'
}

export function InstancesTab({ instances, loading }: { instances: DatabaseInstance[]; loading: boolean }) {
  const { message } = App.useApp()
  const { user } = useAuth()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<DatabaseInstance | null>(null)
  const [modalOpen, setModalOpen] = useState(false)
  const [form] = Form.useForm<CreateDatabaseInstanceRequest>()

  const isAdmin = user?.role === 'admin'

  // What the form asks for depends on the engine: Redis has a numeric DB index
  // rather than a schema, and authenticates with a password alone unless the
  // server runs ACL users.
  const engine = Form.useWatch('engine', form) ?? 'mysql'
  const isRedis = engine === 'redis'

  // Queries are relayed through agenda-node, so a machine without one cannot
  // host a registered database. Filtering here means an operator never picks a
  // machine the server is about to reject.
  const machines = useQuery({ queryKey: ['machines'], queryFn: machineApi.listMachines })
  const agentMachines = (machines.data?.data ?? []).filter((m) => m.mode === 'agent')

  const saveMutation = useMutation({
    mutationFn: (values: CreateDatabaseInstanceRequest) =>
      editing ? api.updateDatabaseInstance(editing.id, values) : api.createDatabaseInstance(values),
    onSuccess: () => {
      message.success(editing ? 'Database updated.' : 'Database registered.')
      queryClient.invalidateQueries({ queryKey: ['db-instances'] })
      closeModal()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.deleteDatabaseInstance(id),
    onSuccess: () => {
      message.success('Database removed.')
      queryClient.invalidateQueries({ queryKey: ['db-instances'] })
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  // Keyed on the instance rather than its id so the result can name the engine
  // it just reached.
  const testMutation = useMutation({
    mutationFn: (inst: DatabaseInstance) => api.testDatabaseInstance(inst.id),
    onSuccess: (res, inst) => {
      if (!res.ok) {
        message.error(res.error ?? 'Could not connect.')
        return
      }
      const server = inst.engine === 'redis' ? 'Redis' : 'MySQL'
      message.success(`Connected${res.server_version ? ` to ${server} ${res.server_version}` : ` to ${server}`}.`)
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  function openCreate() {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({ engine: 'mysql', port: DEFAULT_PORT.mysql, env: 'prod', enabled: true })
    setModalOpen(true)
  }

  function openEdit(inst: DatabaseInstance) {
    setEditing(inst)
    form.resetFields()
    form.setFieldsValue({ ...inst, password: undefined })
    setModalOpen(true)
  }

  function closeModal() {
    setModalOpen(false)
    setEditing(null)
    form.resetFields()
  }

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 16 }}>
        <Typography.Text type="secondary">
          A registered MySQL or Redis sits on its machine — agenda-node connects to it locally, so its port never has to
          be published to the network.
        </Typography.Text>
        {isAdmin && (
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            Register database
          </Button>
        )}
      </div>

      {!isAdmin && (
        <Alert type="info" showIcon message="Registering a database stores its password, so only admins can add or edit one." />
      )}

      <Table<DatabaseInstance>
        rowKey="id"
        loading={loading}
        dataSource={instances}
        pagination={false}
        columns={[
          { title: 'Name', dataIndex: 'name', render: (v) => <strong>{v}</strong> },
          {
            title: 'Engine',
            dataIndex: 'engine',
            render: (v: string) => <Tag color={v === 'redis' ? 'magenta' : 'geekblue'}>{v}</Tag>,
          },
          { title: 'Environment', dataIndex: 'env', render: (v: string) => <Tag color={ENV_COLOR[v]}>{v}</Tag> },
          {
            title: 'Machine',
            key: 'machine',
            render: (_, inst) => {
              const machine = (machines.data?.data ?? []).find((m) => m.id === inst.machine_id)
              return <span className="agenda-mono">{machine?.name ?? `#${inst.machine_id}`}</span>
            },
          },
          { title: 'Port', dataIndex: 'port', render: (v) => <span className="agenda-mono">{v}</span> },
          { title: 'User', dataIndex: 'username', render: (v) => <span className="agenda-mono">{v}</span> },
          {
            title: 'Default schema',
            dataIndex: 'default_database',
            render: (v) => v || <Typography.Text type="secondary">—</Typography.Text>,
          },
          {
            title: 'Status',
            dataIndex: 'enabled',
            render: (v: boolean) => (v ? <Tag color="green">enabled</Tag> : <Tag>disabled</Tag>),
          },
          {
            title: '',
            key: 'actions',
            align: 'right',
            render: (_, inst) =>
              isAdmin ? (
                <Space>
                  <Button
                    size="small"
                    loading={testMutation.isPending && testMutation.variables?.id === inst.id}
                    onClick={() => testMutation.mutate(inst)}
                  >
                    Test
                  </Button>
                  <Button size="small" onClick={() => openEdit(inst)}>
                    Edit
                  </Button>
                  <Popconfirm title="Remove this database?" onConfirm={() => deleteMutation.mutate(inst.id)}>
                    <Button size="small" danger>
                      Remove
                    </Button>
                  </Popconfirm>
                </Space>
              ) : null,
          },
        ]}
      />

      <Modal
        title={editing ? `Edit ${editing.name}` : 'Register database'}
        open={modalOpen}
        onCancel={closeModal}
        onOk={() => form.submit()}
        confirmLoading={saveMutation.isPending}
        destroyOnHidden
      >
        <Form form={form} layout="vertical" requiredMark={false} onFinish={(v) => saveMutation.mutate(v)}>
          <Form.Item name="name" label="Name" rules={[{ required: true }]}>
            <Input placeholder="orders-prod" />
          </Form.Item>
          <Form.Item name="engine" label="Engine">
            <Select
              options={[
                { value: 'mysql', label: 'MySQL' },
                { value: 'redis', label: 'Redis' },
              ]}
              onChange={(v: string) => form.setFieldsValue({ port: DEFAULT_PORT[v] })}
            />
          </Form.Item>
          <Form.Item
            name="machine_id"
            label="Machine"
            rules={[{ required: true }]}
            extra="Only machines running agenda-node can host a queryable database."
          >
            <Select
              loading={machines.isLoading}
              options={agentMachines.map((m) => ({ value: m.id, label: `${m.name} (${m.machine_type})` }))}
              notFoundContent="No agent-mode machines yet."
            />
          </Form.Item>
          <Form.Item name="port" label="Port" rules={[{ required: true }]}>
            <InputNumber min={1} max={65535} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item
            name="username"
            label="Username"
            rules={isRedis ? [] : [{ required: true }]}
            extra={isRedis ? 'Leave blank unless the server runs ACL users; blank means the default user.' : undefined}
          >
            <Input placeholder={isRedis ? 'default' : 'agenda_ro'} autoComplete="off" />
          </Form.Item>
          <Form.Item
            name="password"
            label="Password"
            rules={editing || isRedis ? [] : [{ required: true }]}
            extra={passwordHint(editing !== null, isRedis)}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item
            name="default_database"
            label={isRedis ? 'Default DB index' : 'Default schema'}
            rules={isRedis ? [{ pattern: /^\d+$/, message: 'A Redis default is a numeric database index.' }] : []}
          >
            <Input placeholder={isRedis ? '0' : 'orders'} />
          </Form.Item>
          <Form.Item name="env" label="Environment" extra="Production and staging databases can only be queried by admins.">
            <Select
              options={[
                { value: 'prod', label: 'prod' },
                { value: 'stage', label: 'stage' },
                { value: 'test', label: 'test' },
              ]}
            />
          </Form.Item>
          <Form.Item name="description" label="Description">
            <Input.TextArea autoSize={{ minRows: 2, maxRows: 4 }} />
          </Form.Item>
          <Form.Item name="enabled" label="Enabled" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </Space>
  )
}
