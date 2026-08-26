import { useQuery } from '@tanstack/react-query'
import { Tabs, Typography } from 'antd'
import { useSearchParams } from 'react-router-dom'
import * as api from '@/api/databases'
import { HistoryTab } from './HistoryTab'
import { InstancesTab } from './InstancesTab'
import { SqlConsole } from './SqlConsole'

export function DatabasesPage() {
  // The active tab lives in the URL so a console session can be linked to and
  // survives a reload.
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') ?? 'console'

  const instances = useQuery({ queryKey: ['db-instances'], queryFn: api.listDatabaseInstances })
  const list = instances.data?.data ?? []

  return (
    <div>
      <Typography.Title level={3} className="agenda-display" style={{ marginTop: 0, marginBottom: 20 }}>
        Databases
      </Typography.Title>

      <Tabs
        activeKey={tab}
        onChange={(key) => setParams(key === 'console' ? {} : { tab: key }, { replace: true })}
        items={[
          {
            key: 'console',
            label: 'SQL console',
            children: <SqlConsole instances={list} loading={instances.isLoading} />,
          },
          {
            key: 'instances',
            label: `Instances${list.length ? ` (${list.length})` : ''}`,
            children: <InstancesTab instances={list} loading={instances.isLoading} />,
          },
          {
            key: 'history',
            label: 'Query history',
            children: <HistoryTab />,
          },
        ]}
      />
    </div>
  )
}
