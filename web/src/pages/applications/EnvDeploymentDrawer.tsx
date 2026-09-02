import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Drawer, Space, Table, Tag, Typography } from 'antd'
import * as api from '@/api/releases'
import type { ApplicationRelease, EnvDeploymentStatus } from '@/api/types'
import { StatusPill } from '@/components/StatusPill'
import { errorMessage } from '@/utils/errorMessage'

// A batch is worth polling while it (or any child) could still be moving.
function isInFlight(status: EnvDeploymentStatus): boolean {
  return status === 'pending' || status === 'running'
}

export function EnvDeploymentDrawer({
  deploymentId,
  onClose,
  onOpenRelease,
  onNavigate,
}: {
  deploymentId: number | null
  onClose: () => void
  onOpenRelease: (releaseId: number) => void
  // Called with the replacement batch after a rollback, so the drawer follows
  // the rollout the operator just started.
  onNavigate?: (deploymentId: number) => void
}) {
  const { message, modal } = App.useApp()
  const queryClient = useQueryClient()

  const { data } = useQuery({
    queryKey: ['env-deployments', deploymentId],
    queryFn: () => api.getEnvDeployment(deploymentId!),
    enabled: deploymentId != null,
    refetchInterval: (query) => (query.state.data && isInFlight(query.state.data.status) ? 2000 : false),
  })

  const children = data?.releases ?? []
  const awaitingVerify = children.filter((r) => r.status === 'pending_verify').length
  // Mirrors the server's plan: an instance can be rolled back once its release
  // actually reached it. A batch where every child failed has nothing to undo.
  const rollbackable = children.filter((r) => r.status === 'pending_verify' || r.status === 'verified').length

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['env-deployments'] })
    queryClient.invalidateQueries({ queryKey: ['releases'] })
  }

  const verifyMutation = useMutation({
    mutationFn: () => api.verifyEnvDeployment(deploymentId!),
    onSuccess: () => {
      // awaitingVerify is the pre-click count, which is exactly what the server
      // verifies. Counting 'verified' off the response would instead report
      // every instance in the batch that is now verified, including ones that
      // were already verified individually before this click.
      message.success(`Verified ${awaitingVerify} ${awaitingVerify === 1 ? 'instance' : 'instances'}.`)
      invalidate()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })
  const rollbackMutation = useMutation({
    mutationFn: () => api.rollbackEnvDeployment(deploymentId!),
    onSuccess: (batch) => {
      message.success(
        batch.total_count === 1
          ? 'Rolling back 1 instance.'
          : `Rolling back ${batch.total_count} instances.`,
      )
      invalidate()
      onNavigate?.(batch.id)
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const confirmRollback = () => {
    modal.confirm({
      title: `Roll back ${data?.env ?? ''}?`,
      okText: 'Roll back',
      okButtonProps: { danger: true },
      content: (
        <>
          <p>
            Each of the {rollbackable} affected {rollbackable === 1 ? 'instance' : 'instances'} is redeployed from the
            last verified release it ran before this deploy.
          </p>
          <p style={{ marginBottom: 0 }}>
            Only the <strong>commit</strong> is rolled back — environment variables, deploy configuration and gateway
            routes stay as they are now.
          </p>
        </>
      ),
      onOk: () => rollbackMutation.mutateAsync(),
    })
  }

  return (
    <Drawer
      title={data ? `Env deploy #${data.id}` : 'Env deploy'}
      open={deploymentId != null}
      onClose={onClose}
      size={640}
    >
      {data && (
        <Space direction="vertical" size={20} style={{ width: '100%' }}>
          <div>
            <Space size={8} wrap>
              <Tag>{data.env}</Tag>
              <span className="agenda-mono">{data.branch}</span>
              <StatusPill status={data.status} />
              {data.rollback_of_id > 0 && <Tag color="orange">rollback of #{data.rollback_of_id}</Tag>}
            </Space>
            <div style={{ marginTop: 10 }}>
              <Typography.Text type="secondary">
                {data.success_count}/{data.total_count} succeeded
                {data.failed_count > 0 ? `, ${data.failed_count} failed` : ''}
              </Typography.Text>
            </div>
          </div>

          <div>
            <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 12 }}>
              Instances
            </Typography.Text>
            <Table<ApplicationRelease>
              rowKey="id"
              size="small"
              dataSource={children}
              pagination={false}
              onRow={(record) => ({ onClick: () => onOpenRelease(record.id), style: { cursor: 'pointer' } })}
              columns={[
                {
                  title: 'Instance',
                  dataIndex: 'instance_name',
                  render: (v) => <span className="agenda-mono">{v}</span>,
                },
                {
                  title: 'Commit',
                  dataIndex: 'commit_sha',
                  render: (v: string) => <span className="agenda-mono">{v ? v.slice(0, 10) : '—'}</span>,
                },
                { title: 'Status', dataIndex: 'status', render: (v) => <StatusPill status={v} /> },
              ]}
              locale={{ emptyText: 'No instances in this deploy.' }}
            />
          </div>

          <Space wrap>
            {awaitingVerify > 0 && (
              <Button type="primary" loading={verifyMutation.isPending} onClick={() => verifyMutation.mutate()}>
                {awaitingVerify === 1 ? 'Mark verified' : `Mark all ${awaitingVerify} verified`}
              </Button>
            )}
            {rollbackable > 0 && (
              <Button danger loading={rollbackMutation.isPending} onClick={confirmRollback}>
                Roll back
              </Button>
            )}
          </Space>
        </Space>
      )}
    </Drawer>
  )
}
