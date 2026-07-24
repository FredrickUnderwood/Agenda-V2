import { useQuery } from '@tanstack/react-query'
import { Drawer, Space, Table, Tag, Typography } from 'antd'
import * as api from '@/api/releases'
import type { ApplicationRelease, EnvDeploymentStatus } from '@/api/types'
import { StatusPill } from '@/components/StatusPill'

// A batch is worth polling while it (or any child) could still be moving.
function isInFlight(status: EnvDeploymentStatus): boolean {
  return status === 'pending' || status === 'running'
}

export function EnvDeploymentDrawer({
  deploymentId,
  onClose,
  onOpenRelease,
}: {
  deploymentId: number | null
  onClose: () => void
  onOpenRelease: (releaseId: number) => void
}) {
  const { data } = useQuery({
    queryKey: ['env-deployments', deploymentId],
    queryFn: () => api.getEnvDeployment(deploymentId!),
    enabled: deploymentId != null,
    refetchInterval: (query) => (query.state.data && isInFlight(query.state.data.status) ? 2000 : false),
  })

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
              dataSource={data.releases ?? []}
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
        </Space>
      )}
    </Drawer>
  )
}
