import { useMemo, useState } from 'react'
import { useQueries, useQuery } from '@tanstack/react-query'
import { Segmented, Space, Spin, Typography } from 'antd'
import { PartitionOutlined, UnorderedListOutlined } from '@ant-design/icons'
import * as api from '@/api/applications'
import { RefreshButton } from '@/components/RefreshButton'
import { color } from '@/theme/tokens'
import { buildGatewayModel, type AppInstances } from './model'
import { GatewayTopology } from './GatewayTopology'
import { GatewayTable } from './GatewayTable'

type Mode = 'topology' | 'table'

// Gateway route visualization: aggregates every application's env-scoped routes
// into one request-flow model and renders it either as a Host→Route→Service
// topology (route mode) or a flat route table.
export function GatewayPage() {
  const [mode, setMode] = useState<Mode>('topology')

  const { data: appsResp, isLoading: appsLoading } = useQuery({
    queryKey: ['applications'],
    queryFn: api.listApplications,
  })
  const apps = useMemo(() => appsResp?.data ?? [], [appsResp])

  // One instances query per app (parallel); the queryKey matches RoutesTab's, so
  // editing a route there and coming here shows fresh data from the shared cache.
  const instanceQueries = useQueries({
    queries: apps.map((app) => ({
      queryKey: ['applications', app.id, 'instances'],
      queryFn: () => api.listApplicationInstances(app.id),
    })),
  })

  const instancesLoading = instanceQueries.some((q) => q.isLoading)
  const loading = appsLoading || (apps.length > 0 && instancesLoading)

  const model = useMemo(() => {
    const entries: AppInstances[] = apps.map((app, i) => ({ app, targets: instanceQueries[i]?.data?.data ?? [] }))
    return buildGatewayModel(entries)
    // instanceQueries is a fresh array each render; key on the loaded data instead.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apps, instanceQueries.map((q) => q.dataUpdatedAt).join(',')])

  const enabledRoutes = model.routes.filter((r) => r.enabled).length
  const orphanRoutes = model.routes.filter((r) => r.instances.length === 0).length

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 20 }}>
        <div>
          <Typography.Title level={3} className="agenda-display" style={{ margin: 0 }}>
            Gateway routes
          </Typography.Title>
          <Typography.Text type="secondary">
            Which inbound hosts and paths route to which services — across every application and environment.
          </Typography.Text>
        </div>
        <Space>
          <Segmented<Mode>
            value={mode}
            onChange={setMode}
            options={[
              { value: 'topology', label: 'Route map', icon: <PartitionOutlined /> },
              { value: 'table', label: 'Table', icon: <UnorderedListOutlined /> },
            ]}
          />
          <RefreshButton queryKeys={[['applications']]} />
        </Space>
      </div>

      <Space size={24} style={{ marginBottom: 20 }}>
        <Stat label="Routes" value={`${model.routes.length}`} />
        <Stat label="Enabled" value={`${enabledRoutes}`} />
        <Stat label="Hosts" value={`${model.hosts.length}`} />
        <Stat label="Services" value={`${model.services.length}`} />
        {orphanRoutes > 0 ? <Stat label="No backend" value={`${orphanRoutes}`} tone={color.fail} /> : null}
      </Space>

      {loading ? (
        <div style={{ padding: '80px 0', textAlign: 'center' }}>
          <Spin />
        </div>
      ) : mode === 'topology' ? (
        <GatewayTopology model={model} />
      ) : (
        <GatewayTable model={model} />
      )}
    </div>
  )
}

function Stat({ label, value, tone }: { label: string; value: string; tone?: string }) {
  return (
    <div>
      <div className="agenda-display" style={{ fontSize: 22, fontWeight: 600, color: tone ?? color.ink900, lineHeight: 1.1 }}>
        {value}
      </div>
      <div style={{ fontSize: 12, color: color.ink500 }}>{label}</div>
    </div>
  )
}
