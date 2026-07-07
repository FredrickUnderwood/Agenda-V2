import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Form, Input, Modal, Popconfirm, Select, Table, Tag, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import * as api from '@/api/users'
import type { CreateUserRequest, User } from '@/api/types'
import { errorMessage } from '@/utils/errorMessage'

export function UsersPage() {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [modalOpen, setModalOpen] = useState(false)
  const [form] = Form.useForm<CreateUserRequest>()

  const { data, isLoading } = useQuery({ queryKey: ['users'], queryFn: api.listUsers })

  const createMutation = useMutation({
    mutationFn: (req: CreateUserRequest) => api.createUser(req),
    onSuccess: () => {
      message.success('User created.')
      queryClient.invalidateQueries({ queryKey: ['users'] })
      setModalOpen(false)
      form.resetFields()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.deleteUser(id),
    onSuccess: () => {
      message.success('User removed.')
      queryClient.invalidateQueries({ queryKey: ['users'] })
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
        <Typography.Title level={3} className="agenda-display" style={{ margin: 0 }}>
          Users
        </Typography.Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setModalOpen(true)}>
          New user
        </Button>
      </div>

      <Table<User>
        rowKey="id"
        loading={isLoading}
        dataSource={data?.data ?? []}
        pagination={false}
        columns={[
          { title: 'Username', dataIndex: 'username', render: (v) => <strong>{v}</strong> },
          { title: 'Display name', dataIndex: 'display_name' },
          { title: 'Role', dataIndex: 'role', render: (v) => <Tag color={v === 'admin' ? 'gold' : 'default'}>{v}</Tag> },
          { title: 'Active', dataIndex: 'is_active', render: (v: boolean) => (v ? 'Yes' : 'No') },
          {
            title: '',
            key: 'actions',
            render: (_, u) => (
              <Popconfirm title="Remove this user?" onConfirm={() => deleteMutation.mutate(u.id)}>
                <Button size="small" danger>
                  Remove
                </Button>
              </Popconfirm>
            ),
          },
        ]}
        locale={{ emptyText: 'No users yet.' }}
      />

      <Modal
        title="New user"
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={() => form.submit()}
        confirmLoading={createMutation.isPending}
        okText="Create"
      >
        <Form form={form} layout="vertical" requiredMark={false} initialValues={{ role: 'member' }} onFinish={(v) => createMutation.mutate(v)}>
          <Form.Item name="username" label="Username" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="password" label="Password" rules={[{ required: true, min: 8 }]}>
            <Input.Password />
          </Form.Item>
          <Form.Item name="display_name" label="Display name">
            <Input />
          </Form.Item>
          <Form.Item name="role" label="Role">
            <Select
              options={[
                { value: 'member', label: 'Member' },
                { value: 'admin', label: 'Admin' },
              ]}
            />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
