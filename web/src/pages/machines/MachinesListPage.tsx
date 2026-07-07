import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Form, Input, InputNumber, Modal, Popconfirm, Select, Table, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import * as api from '@/api/machines'
import type { CreateMachineRequest, Machine } from '@/api/types'
import { StatusPill } from '@/components/StatusPill'
import { errorMessage } from '@/utils/errorMessage'

export function MachinesListPage() {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [modalOpen, setModalOpen] = useState(false)
  const [form] = Form.useForm<CreateMachineRequest>()
  const mode = Form.useWatch('mode', form)

  const { data, isLoading } = useQuery({ queryKey: ['machines'], queryFn: api.listMachines })

  const createMutation = useMutation({
    mutationFn: (req: CreateMachineRequest) => api.createMachine(req),
    onSuccess: () => {
      message.success('Machine added.')
      queryClient.invalidateQueries({ queryKey: ['machines'] })
      setModalOpen(false)
      form.resetFields()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.deleteMachine(id),
    onSuccess: () => {
      message.success('Machine removed.')
      queryClient.invalidateQueries({ queryKey: ['machines'] })
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const testMutation = useMutation({
    mutationFn: (id: number) => api.testMachineConnection(id),
    onSuccess: (res) => (res.ok ? message.success('Connection OK.') : message.error(res.error ?? 'Connection failed.')),
  })

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
        <Typography.Title level={3} className="agenda-display" style={{ margin: 0 }}>
          Machines
        </Typography.Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setModalOpen(true)}>
          Add machine
        </Button>
      </div>

      <Table<Machine>
        rowKey="id"
        loading={isLoading}
        dataSource={data?.data ?? []}
        pagination={false}
        columns={[
          { title: 'Name', dataIndex: 'name', render: (v) => <strong>{v}</strong> },
          { title: 'Type', dataIndex: 'machine_type' },
          { title: 'Mode', dataIndex: 'mode' },
          {
            title: 'Address',
            key: 'address',
            render: (_, m) => <span className="agenda-mono">{m.mode === 'agent' ? m.agent_base_url : `${m.host}:${m.port}`}</span>,
          },
          {
            title: 'Status',
            key: 'status',
            render: (_, m) =>
              m.mode === 'agent' ? (
                <StatusPill status={m.online ? 'online' : 'offline'} />
              ) : (
                <Button size="small" onClick={() => testMutation.mutate(m.id)} loading={testMutation.isPending && testMutation.variables === m.id}>
                  Test connection
                </Button>
              ),
          },
          {
            title: '',
            key: 'actions',
            render: (_, m) => (
              <Popconfirm title="Remove this machine?" onConfirm={() => deleteMutation.mutate(m.id)}>
                <Button size="small" danger>
                  Remove
                </Button>
              </Popconfirm>
            ),
          },
        ]}
        locale={{ emptyText: 'No machines yet — add one to deploy applications onto it.' }}
      />

      <Modal
        title="Add machine"
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={() => form.submit()}
        confirmLoading={createMutation.isPending}
        okText="Add"
      >
        <Form form={form} layout="vertical" requiredMark={false} initialValues={{ machine_type: 'prod', mode: 'ssh', port: 22 }} onFinish={(v) => createMutation.mutate(v)}>
          <Form.Item name="name" label="Name" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="machine_type" label="Environment type" rules={[{ required: true }]}>
            <Select
              options={[
                { value: 'prod', label: 'Production' },
                { value: 'stage', label: 'Staging' },
                { value: 'test', label: 'Test' },
              ]}
            />
          </Form.Item>
          <Form.Item name="mode" label="Reach this machine via">
            <Select
              options={[
                { value: 'ssh', label: 'SSH' },
                { value: 'agent', label: 'agenda-node agent' },
              ]}
            />
          </Form.Item>

          {mode === 'agent' ? (
            <>
              <Form.Item name="agent_base_url" label="Agent base URL" rules={[{ required: true }]} extra="http://host:7100">
                <Input className="agenda-mono" />
              </Form.Item>
              <Form.Item name="agent_proxy_base_url" label="Agent proxy base URL" extra="http://host:7200">
                <Input className="agenda-mono" />
              </Form.Item>
              <Form.Item name="agent_token" label="Agent token" rules={[{ required: true }]}>
                <Input.Password />
              </Form.Item>
            </>
          ) : (
            <>
              <Form.Item name="host" label="Host" rules={[{ required: true }]}>
                <Input placeholder="10.0.0.1" />
              </Form.Item>
              <Form.Item name="port" label="SSH port">
                <InputNumber min={1} max={65535} style={{ width: '100%' }} />
              </Form.Item>
              <Form.Item name="user" label="SSH user">
                <Input placeholder="root" />
              </Form.Item>
              <Form.Item name="auth_type" label="Auth type" initialValue="sshkey">
                <Select
                  options={[
                    { value: 'sshkey', label: 'SSH key' },
                    { value: 'password', label: 'Password' },
                  ]}
                />
              </Form.Item>
              <Form.Item name="ssh_key_path" label="SSH key path">
                <Input placeholder="~/.ssh/id_ed25519" />
              </Form.Item>
              <Form.Item name="password" label="Password">
                <Input.Password />
              </Form.Item>
            </>
          )}
          <Form.Item name="workspace_root" label="Workspace root" extra="Absolute path where repos are checked out on this machine.">
            <Input placeholder="/root/.agenda-v2/workspaces" className="agenda-mono" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
