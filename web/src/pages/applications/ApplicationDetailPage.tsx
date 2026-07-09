import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Breadcrumb, Button, Popconfirm, Spin, Tabs, Tag, Typography } from 'antd'
import { useNavigate, useParams } from 'react-router-dom'
import * as api from '@/api/applications'
import { color } from '@/theme/tokens'
import { errorMessage } from '@/utils/errorMessage'
import { InstancesTab } from './InstancesTab'
import { MonitoringTab } from './MonitoringTab'
import { OverviewTab } from './OverviewTab'
import { ReleasesTab } from './ReleasesTab'
import { RoutesTab } from './RoutesTab'

export function ApplicationDetailPage() {
  const { message } = App.useApp()
  const { appId } = useParams<{ appId: string }>()
  const id = Number(appId)
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const { data: app, isLoading } = useQuery({
    queryKey: ['applications', id],
    queryFn: () => api.getApplication(id),
    enabled: !Number.isNaN(id),
  })

  const deleteMutation = useMutation({
    mutationFn: () => api.deleteApplication(id),
    onSuccess: () => {
      message.success('Application deleted.')
      queryClient.invalidateQueries({ queryKey: ['applications'] })
      navigate('/applications')
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  if (isLoading || !app) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', padding: 64 }}>
        <Spin size="large" />
      </div>
    )
  }

  return (
    <div>
      <Breadcrumb
        items={[{ title: <a onClick={() => navigate('/applications')}>Applications</a> }, { title: app.name }]}
        style={{ marginBottom: 12 }}
      />
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 20 }}>
        <div>
          <Typography.Title level={3} className="agenda-display" style={{ margin: 0 }}>
            {app.name}
          </Typography.Title>
          <div style={{ marginTop: 6, display: 'flex', gap: 8, alignItems: 'center' }}>
            <Tag>{app.deploy_method}</Tag>
            <span className="agenda-mono" style={{ fontSize: 12, color: color.ink500 }}>
              {app.repo_url}
            </span>
          </div>
        </div>
        <Popconfirm title="Delete this application?" description="This cannot be undone." onConfirm={() => deleteMutation.mutate()}>
          <Button danger loading={deleteMutation.isPending}>
            Delete
          </Button>
        </Popconfirm>
      </div>

      <Tabs
        defaultActiveKey="instances"
        items={[
          { key: 'instances', label: 'Instances', children: <InstancesTab appId={id} /> },
          { key: 'routes', label: 'Routes', children: <RoutesTab appId={id} /> },
          { key: 'releases', label: 'Releases', children: <ReleasesTab appId={id} /> },
          { key: 'monitoring', label: 'Monitoring', children: <MonitoringTab serviceName={app.name} /> },
          { key: 'overview', label: 'Settings', children: <OverviewTab app={app} /> },
        ]}
      />
    </div>
  )
}
