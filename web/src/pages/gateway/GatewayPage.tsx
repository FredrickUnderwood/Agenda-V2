import { useMemo, useState } from 'react'
import { useQueries, useQuery } from '@tanstack/react-query'
import { Segmented, Space, Spin, Tabs, Typography } from 'antd'
import { PartitionOutlined, UnorderedListOutlined } from '@ant-design/icons'
import * as api from '@/api/applications'
import { getConfig } from '@/api/config'
import { RefreshButton } from '@/components/RefreshButton'
import type { Environment } from '@/api/types'
import { color } from '@/theme/tokens'
import {
  buildGatewayModel,
  envsInModel,
  filterModelByEnv,
  summarize,
  type AppInstances,
  type GatewayModel,
} from './model'
import { GatewayTopology } from './GatewayTopology'
import { GatewayTable } from './GatewayTable'
import { GatewayStatusBar, HealthChips, StatusDot } from './GatewayHealth'

type Mode = 'topology' | 'table'
type EnvKey = Environment | 'all'

const ENV_LABEL: Record<Environment, string> = { prod: 'Prod', stage: 'Stage', test: 'Test' }

// Gateway route visualization: aggregates every application's env-scoped routes
// into one request-flow model, shows an overall health rollup, and renders each
// environment's route map on its own tab — as a Host→Route→Service topology or a
// flat route table.
export function GatewayPage() {
  const [mode, setMode] = useState<Mode>('topology')
  const [selectedEnv, setSelectedEnv] = useState<EnvKey | null>(null)

  const { data: appsResp, isLoading: appsLoading } = useQuery({
    queryKey: ['applications'],
    queryFn: api.listApplications,
  })
  const { data: config } = useQuery({ queryKey: ['config'], queryFn: getConfig })
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

  const overall = useMemo(() => summarize(model), [model])
  const envs = useMemo(() => envsInModel(model), [model])

  // Tab keys: one per present env, plus "All" once more than one env exists.
  const tabKeys: EnvKey[] = useMemo(() => (envs.length > 1 ? [...envs, 'all'] : envs), [envs])
  const activeEnv: EnvKey = selectedEnv && tabKeys.includes(selectedEnv) ? selectedEnv : (tabKeys[0] ?? 'all')

  const renderView = (m: GatewayModel) => (mode === 'topology' ? <GatewayTopology model={m} /> : <GatewayTable model={m} />)

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 20, gap: 16, flexWrap: 'wrap' }}>
        <div>
          <Typography.Title level={3} className="agenda-display" style={{ margin: 0 }}>
            Gateway routes
          </Typography.Title>
          <Typography.Text type="secondary">
            Which inbound hosts and paths route to which services — with backend health, per environment.
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
          <RefreshButton queryKeys={[['applications'], ['config']]} />
        </Space>
      </div>

      {loading ? (
        <div style={{ padding: '80px 0', textAlign: 'center' }}>
          <Spin />
        </div>
      ) : (
        <>
          <GatewayStatusBar summary={overall} gatewayEnabled={config?.gateway.enabled} baseUrl={config?.gateway.base_url} envCount={envs.length} />

          {tabKeys.length === 0 ? (
            // No routes anywhere — let the active view render its own empty state.
            renderView(model)
          ) : (
            <Tabs
              activeKey={activeEnv}
              onChange={(k) => setSelectedEnv(k as EnvKey)}
              items={tabKeys.map((key) => {
                const m = filterModelByEnv(model, key)
                const s = summarize(m)
                return {
                  key,
                  label: <StatusDot status={s.status} label={key === 'all' ? 'All' : ENV_LABEL[key]} />,
                  children: (
                    <div>
                      <div style={{ marginBottom: 16, paddingBottom: 14, borderBottom: `1px solid ${color.paperBorder}` }}>
                        <HealthChips summary={s} />
                      </div>
                      {renderView(m)}
                    </div>
                  ),
                }
              })}
            />
          )}
        </>
      )}
    </div>
  )
}
