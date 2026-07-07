import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Form, Input, Modal, Select, Table, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import * as api from '@/api/applications'
import type { Application, CreateApplicationRequest } from '@/api/types'
import { color, font } from '@/theme/tokens'
import { errorMessage } from '@/utils/errorMessage'

export function ApplicationsListPage() {
  const { message } = App.useApp()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [modalOpen, setModalOpen] = useState(false)
  const [form] = Form.useForm<CreateApplicationRequest>()

  const { data, isLoading } = useQuery({ queryKey: ['applications'], queryFn: api.listApplications })

  const createMutation = useMutation({
    mutationFn: (req: CreateApplicationRequest) => api.createApplication(req),
    onSuccess: (app) => {
      message.success(`${app.name} created.`)
      queryClient.invalidateQueries({ queryKey: ['applications'] })
      setModalOpen(false)
      form.resetFields()
      navigate(`/applications/${app.id}`)
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
        <Typography.Title level={3} className="agenda-display" style={{ margin: 0 }}>
          Applications
        </Typography.Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setModalOpen(true)}>
          New application
        </Button>
      </div>

      <Table<Application>
        rowKey="id"
        loading={isLoading}
        dataSource={data?.data ?? []}
        onRow={(record) => ({ onClick: () => navigate(`/applications/${record.id}`), style: { cursor: 'pointer' } })}
        pagination={false}
        columns={[
          { title: 'Name', dataIndex: 'name', render: (v) => <strong>{v}</strong> },
          {
            title: 'Repository',
            dataIndex: 'repo_url',
            render: (v) => <span className="agenda-mono" style={{ fontSize: 12, color: color.ink500 }}>{v}</span>,
          },
          { title: 'Deploy method', dataIndex: 'deploy_method' },
          { title: 'Description', dataIndex: 'description', ellipsis: true },
        ]}
        locale={{ emptyText: 'No applications yet — create one to point agenda at a repo.' }}
      />

      <Modal
        title="New application"
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={() => form.submit()}
        confirmLoading={createMutation.isPending}
        okText="Create"
      >
        <Form form={form} layout="vertical" onFinish={(v) => createMutation.mutate(v)} requiredMark={false}>
          <Form.Item name="name" label="Name" rules={[{ required: true }]}>
            <Input placeholder="my-api" />
          </Form.Item>
          <Form.Item
            name="repo_url"
            label="Repository URL"
            rules={[{ required: true }]}
            extra="git@github.com:you/repo.git or https://github.com/you/repo.git"
          >
            <Input placeholder="git@github.com:you/repo.git" style={{ fontFamily: font.mono }} />
          </Form.Item>
          <Form.Item name="deploy_method" label="Deploy method" rules={[{ required: true }]} initialValue="docker">
            <Select
              options={[
                { value: 'docker', label: 'Docker Compose' },
                { value: 'api', label: 'HTTP webhook' },
              ]}
            />
          </Form.Item>
          <Form.Item name="description" label="Description">
            <Input.TextArea rows={2} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
