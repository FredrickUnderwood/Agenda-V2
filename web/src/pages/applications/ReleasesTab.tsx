import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Form, Input, Modal, Select, Table, Tag } from 'antd'
import { PlusOutlined, RocketOutlined } from '@ant-design/icons'
import * as api from '@/api/releases'
import { listApplicationInstances } from '@/api/applications'
import type {
  ApplicationRelease,
  CreateEnvDeploymentRequest,
  CreateReleaseRequest,
  Environment,
  EnvDeployment,
} from '@/api/types'
import { StatusPill } from '@/components/StatusPill'
import { RefreshButton } from '@/components/RefreshButton'
import { errorMessage } from '@/utils/errorMessage'
import { ReleaseDetailDrawer } from './ReleaseDetailDrawer'
import { EnvDeploymentDrawer } from './EnvDeploymentDrawer'

const ENV_OPTIONS: { value: Environment; label: string }[] = [
  { value: 'prod', label: 'Production' },
  { value: 'stage', label: 'Staging' },
  { value: 'test', label: 'Test' },
]

export function ReleasesTab({ appId }: { appId: number }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [modalOpen, setModalOpen] = useState(false)
  const [envModalOpen, setEnvModalOpen] = useState(false)
  const [openReleaseId, setOpenReleaseId] = useState<number | null>(null)
  const [openDeploymentId, setOpenDeploymentId] = useState<number | null>(null)
  const [form] = Form.useForm<CreateReleaseRequest>()
  const [envForm] = Form.useForm<CreateEnvDeploymentRequest>()

  // The New-release form's instance dropdown is scoped to whichever env is
  // currently selected in the form, so you only ever pick a real instance.
  const releaseEnv = Form.useWatch('env', form) as Environment | undefined

  const { data, isLoading } = useQuery({
    queryKey: ['releases', 'byApp', appId],
    queryFn: () => api.listReleases(appId),
  })
  const { data: envDeployments } = useQuery({
    queryKey: ['env-deployments', 'byApp', appId],
    queryFn: () => api.listEnvDeployments(appId),
  })
  const { data: instances } = useQuery({
    queryKey: ['applications', appId, 'instances'],
    queryFn: () => listApplicationInstances(appId),
  })

  const instanceOptions = (instances?.data ?? [])
    .filter((t) => (releaseEnv ? t.env === releaseEnv : true) && t.enabled)
    .map((t) => ({ value: t.instance_name, label: t.instance_name }))

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

  const envDeployMutation = useMutation({
    mutationFn: (req: CreateEnvDeploymentRequest) => api.createEnvDeployment(appId, req),
    onSuccess: (batch) => {
      message.success(`Deploying ${batch.total_count} instance(s) in ${batch.env}.`)
      queryClient.invalidateQueries({ queryKey: ['env-deployments', 'byApp', appId] })
      queryClient.invalidateQueries({ queryKey: ['releases', 'byApp', appId] })
      setEnvModalOpen(false)
      envForm.resetFields()
      setOpenDeploymentId(batch.id)
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginBottom: 12 }}>
        <RefreshButton
          queryKeys={[
            ['releases', 'byApp', appId],
            ['env-deployments', 'byApp', appId],
            ['applications', appId, 'instances'],
          ]}
        />
        <Button icon={<RocketOutlined />} onClick={() => setEnvModalOpen(true)}>
          Deploy environment
        </Button>
        <Button icon={<PlusOutlined />} onClick={() => setModalOpen(true)}>
          New release
        </Button>
      </div>

      <Table<EnvDeployment>
        rowKey="id"
        title={() => 'Environment deployments'}
        dataSource={envDeployments?.data ?? []}
        pagination={false}
        style={{ marginBottom: 24 }}
        onRow={(record) => ({ onClick: () => setOpenDeploymentId(record.id), style: { cursor: 'pointer' } })}
        columns={[
          { title: 'Env', dataIndex: 'env', render: (v) => <Tag>{v}</Tag> },
          { title: 'Branch', dataIndex: 'branch', render: (v) => <span className="agenda-mono">{v}</span> },
          {
            title: 'Instances',
            key: 'instances',
            render: (_, r) => (
              <span className="agenda-mono">
                {r.success_count}/{r.total_count} ok{r.failed_count > 0 ? `, ${r.failed_count} failed` : ''}
              </span>
            ),
          },
          { title: 'Status', dataIndex: 'status', render: (v) => <StatusPill status={v} /> },
          { title: 'Started', dataIndex: 'started_at', render: (v: string) => new Date(v).toLocaleString() },
        ]}
        locale={{ emptyText: 'No environment-wide deploys yet.' }}
      />

      <Table<ApplicationRelease>
        rowKey="id"
        title={() => 'Releases'}
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
          {
            title: 'Batch',
            dataIndex: 'env_deployment_id',
            render: (v: number) =>
              v > 0 ? (
                <a
                  onClick={(e) => {
                    e.stopPropagation()
                    setOpenDeploymentId(v)
                  }}
                >
                  #{v}
                </a>
              ) : (
                '—'
              ),
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
          initialValues={{ env: 'prod', instance_name: 'default', branch: 'master' }}
          onFinish={(v) => createMutation.mutate(v)}
        >
          <Form.Item name="env" label="Environment" rules={[{ required: true }]}>
            <Select options={ENV_OPTIONS} />
          </Form.Item>
          <Form.Item name="instance_name" label="Instance name">
            <Select
              showSearch
              placeholder="default"
              options={instanceOptions}
              notFoundContent="No enabled instances in this env"
            />
          </Form.Item>
          <Form.Item name="branch" label="Branch" rules={[{ required: true }]}>
            <Input placeholder="master" className="agenda-mono" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="Deploy environment"
        open={envModalOpen}
        onCancel={() => setEnvModalOpen(false)}
        onOk={() => envForm.submit()}
        confirmLoading={envDeployMutation.isPending}
        okText="Deploy all instances"
      >
        <p style={{ marginTop: 0, color: 'var(--agenda-text-secondary, #888)' }}>
          Deploys the branch to <strong>every enabled instance</strong> of the selected environment as a single deploy
          record.
        </p>
        <Form
          form={envForm}
          layout="vertical"
          requiredMark={false}
          initialValues={{ env: 'prod', branch: 'master' }}
          onFinish={(v) => envDeployMutation.mutate(v)}
        >
          <Form.Item name="env" label="Environment" rules={[{ required: true }]}>
            <Select options={ENV_OPTIONS} />
          </Form.Item>
          <Form.Item name="branch" label="Branch">
            <Input placeholder="master" className="agenda-mono" />
          </Form.Item>
        </Form>
      </Modal>

      <ReleaseDetailDrawer releaseId={openReleaseId} onClose={() => setOpenReleaseId(null)} />
      <EnvDeploymentDrawer
        deploymentId={openDeploymentId}
        onClose={() => setOpenDeploymentId(null)}
        onOpenRelease={(id) => {
          setOpenDeploymentId(null)
          setOpenReleaseId(id)
        }}
      />
    </div>
  )
}
