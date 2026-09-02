import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Form, Input, Modal, Select, Space, Table, Tag } from 'antd'
import { RocketOutlined } from '@ant-design/icons'
import * as api from '@/api/releases'
import { listApplicationInstances } from '@/api/applications'
import type { ApplicationRelease, CreateEnvDeploymentRequest, Environment, EnvDeployment } from '@/api/types'
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

// The Deploy form: an empty instance means "all enabled instances of the env".
interface DeployFormValues extends CreateEnvDeploymentRequest {
  instance_name?: string
}

export function ReleasesTab({ appId }: { appId: number }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [deployOpen, setDeployOpen] = useState(false)
  const [openReleaseId, setOpenReleaseId] = useState<number | null>(null)
  const [openDeploymentId, setOpenDeploymentId] = useState<number | null>(null)
  const [form] = Form.useForm<DeployFormValues>()

  // The instance dropdown is scoped to whichever env is selected in the form.
  const deployEnv = Form.useWatch('env', form) as Environment | undefined

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
    .filter((t) => (deployEnv ? t.env === deployEnv : true) && t.enabled)
    // A stopped instance is still deployable — deploying it is how you restart a
    // decommissioned instance — so annotate it rather than hide it. (An env-wide
    // deploy with no instance chosen deliberately skips stopped ones server-side.)
    .map((t) => ({
      value: t.instance_name,
      label: t.desired_state === 'stopped' ? `${t.instance_name} (stopped — will restart)` : t.instance_name,
    }))

  const deployMutation = useMutation({
    mutationFn: (req: CreateEnvDeploymentRequest) => api.createEnvDeployment(appId, req),
    onSuccess: (batch) => {
      message.success(
        batch.total_count === 1
          ? `Deploying ${batch.releases?.[0]?.instance_name ?? '1 instance'} in ${batch.env}.`
          : `Deploying ${batch.total_count} instances in ${batch.env}.`,
      )
      queryClient.invalidateQueries({ queryKey: ['env-deployments', 'byApp', appId] })
      queryClient.invalidateQueries({ queryKey: ['releases', 'byApp', appId] })
      setDeployOpen(false)
      form.resetFields()
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
        <Button type="primary" icon={<RocketOutlined />} onClick={() => setDeployOpen(true)}>
          Deploy
        </Button>
      </div>

      <Table<EnvDeployment>
        rowKey="id"
        title={() => 'Deployments'}
        dataSource={envDeployments?.data ?? []}
        pagination={false}
        style={{ marginBottom: 24 }}
        onRow={(record) => ({ onClick: () => setOpenDeploymentId(record.id), style: { cursor: 'pointer' } })}
        columns={[
          {
            title: 'Env',
            dataIndex: 'env',
            render: (v: Environment, r: EnvDeployment) => (
              <Space size={4}>
                <Tag>{v}</Tag>
                {r.rollback_of_id > 0 && <Tag color="orange">rollback</Tag>}
              </Space>
            ),
          },
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
        locale={{ emptyText: 'No deployments yet — click Deploy to roll out a branch.' }}
      />

      <Table<ApplicationRelease>
        rowKey="id"
        title={() => 'Deployment Detail'}
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
            title: 'Deployment',
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
        locale={{ emptyText: 'No deployment detail yet.' }}
      />

      <Modal
        title="Deploy"
        open={deployOpen}
        onCancel={() => setDeployOpen(false)}
        onOk={() => form.submit()}
        confirmLoading={deployMutation.isPending}
        okText="Deploy"
      >
        <p style={{ marginTop: 0, color: 'var(--agenda-ink-500, #888)' }}>
          Deploys the branch to the selected environment as a single deployment record. Leave <strong>Instance</strong>{' '}
          empty to deploy <strong>all enabled instances</strong> of the environment.
        </p>
        <Form
          form={form}
          layout="vertical"
          requiredMark={false}
          initialValues={{ env: 'prod', branch: 'master' }}
          onFinish={(v) => deployMutation.mutate(v)}
        >
          <Form.Item name="env" label="Environment" rules={[{ required: true }]}>
            <Select options={ENV_OPTIONS} />
          </Form.Item>
          <Form.Item name="instance_name" label="Instance" tooltip="Leave empty to deploy all enabled instances">
            <Select
              allowClear
              showSearch
              placeholder="All instances"
              options={instanceOptions}
              notFoundContent="No enabled instances in this env"
            />
          </Form.Item>
          <Form.Item name="branch" label="Branch">
            <Input placeholder="master" className="agenda-mono" />
          </Form.Item>
          <Form.Item
            name="commit_sha"
            label="Commit"
            tooltip="Leave empty to deploy the branch's latest commit"
            rules={[
              {
                // Git resolves an abbreviation as happily as the full object
                // name, so both are accepted; what gets recorded on the release
                // is always the full SHA the checkout resolved to. Same bounds
                // as git.NormalizeCommitSHA on the server.
                pattern: /^[0-9a-fA-F]{7,40}$/,
                message: '7–40 hex characters, e.g. 8ce16504d4 or the full 40-character SHA',
              },
            ]}
          >
            <Input allowClear placeholder="Latest commit on the branch" className="agenda-mono" />
          </Form.Item>
        </Form>
      </Modal>

      <ReleaseDetailDrawer
        releaseId={openReleaseId}
        onClose={() => setOpenReleaseId(null)}
        onNavigate={setOpenReleaseId}
      />
      <EnvDeploymentDrawer
        deploymentId={openDeploymentId}
        onClose={() => setOpenDeploymentId(null)}
        onOpenRelease={(id) => {
          setOpenDeploymentId(null)
          setOpenReleaseId(id)
        }}
        onNavigate={setOpenDeploymentId}
      />
    </div>
  )
}
