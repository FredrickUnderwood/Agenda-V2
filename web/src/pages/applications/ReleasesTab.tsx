import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Form, Input, Modal, Select, Table, Tag } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import * as api from '@/api/releases'
import type { ApplicationRelease, CreateReleaseRequest } from '@/api/types'
import { StatusPill } from '@/components/StatusPill'
import { errorMessage } from '@/utils/errorMessage'
import { ReleaseDetailDrawer } from './ReleaseDetailDrawer'

export function ReleasesTab({ appId }: { appId: number }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [modalOpen, setModalOpen] = useState(false)
  const [openReleaseId, setOpenReleaseId] = useState<number | null>(null)
  const [form] = Form.useForm<CreateReleaseRequest>()

  const { data, isLoading } = useQuery({
    queryKey: ['releases', 'byApp', appId],
    queryFn: () => api.listReleases(appId),
  })

  const createMutation = useMutation({
    mutationFn: (req: CreateReleaseRequest) => api.createRelease(appId, req),
    onSuccess: (rel) => {
      message.success('Release created as a draft.')
      queryClient.invalidateQueries({ queryKey: ['releases', 'byApp', appId] })
      setModalOpen(false)
      form.resetFields()
      setOpenReleaseId(rel.id)
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12 }}>
        <Button icon={<PlusOutlined />} onClick={() => setModalOpen(true)}>
          New release
        </Button>
      </div>

      <Table<ApplicationRelease>
        rowKey="id"
        loading={isLoading}
        dataSource={data?.data ?? []}
        pagination={false}
        onRow={(record) => ({ onClick: () => setOpenReleaseId(record.id), style: { cursor: 'pointer' } })}
        columns={[
          { title: 'Env', dataIndex: 'env', render: (v) => <Tag>{v}</Tag> },
          { title: 'Instance', dataIndex: 'instance_name', render: (v) => <span className="agenda-mono">{v}</span> },
          { title: 'Branch', dataIndex: 'branch', render: (v) => <span className="agenda-mono">{v}</span> },
          {
            title: 'Commit',
            dataIndex: 'commit_sha',
            render: (v: string) => <span className="agenda-mono">{v ? v.slice(0, 10) : '—'}</span>,
          },
          { title: 'Status', dataIndex: 'status', render: (v) => <StatusPill status={v} /> },
          { title: 'Created', dataIndex: 'created_at', render: (v: string) => new Date(v).toLocaleString() },
        ]}
        locale={{ emptyText: 'No releases yet — create one to deploy a branch.' }}
      />

      <Modal
        title="New release"
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={() => form.submit()}
        confirmLoading={createMutation.isPending}
        okText="Create"
      >
        <Form
          form={form}
          layout="vertical"
          requiredMark={false}
          initialValues={{ env: 'prod', instance_name: 'default' }}
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
          <Form.Item name="instance_name" label="Instance name">
            <Input placeholder="default" />
          </Form.Item>
          <Form.Item name="branch" label="Branch" rules={[{ required: true }]}>
            <Input placeholder="main" className="agenda-mono" />
          </Form.Item>
        </Form>
      </Modal>

      <ReleaseDetailDrawer releaseId={openReleaseId} onClose={() => setOpenReleaseId(null)} />
    </div>
  )
}
