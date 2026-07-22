import { useState } from 'react'
import { Alert, Select, Space, Typography } from 'antd'
import { DEFAULT_TIME_RANGE_FROM, getGrafanaBaseUrl, panelEmbedUrl, TIME_RANGE_OPTIONS } from '@/utils/grafana'

// panelId → title, matching deploy/observability/grafana/dashboards/gateway-overview.json.
// 1/2 are route-level; 3/4/5 are per-endpoint (normalized app-relative path).
const ROUTE_PANELS = [
  { id: 1, title: 'Error rate (5xx) by route' },
  { id: 2, title: 'P99 latency by route' },
] as const

const ENDPOINT_PANELS = [
  { id: 3, title: 'QPS by endpoint' },
  { id: 4, title: 'Error rate (5xx) by endpoint' },
  { id: 5, title: 'Latency P50 / P95 / P99 by endpoint' },
] as const

function Panel({ baseUrl, id, title, from, serviceName, full }: {
  baseUrl: string
  id: number
  title: string
  from: string
  serviceName: string
  full?: boolean
}) {
  return (
    <div style={full ? { gridColumn: '1 / -1' } : undefined}>
      <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 8 }}>
        {title}
      </Typography.Text>
      <iframe
        title={title}
        src={panelEmbedUrl(baseUrl, id, from, serviceName)}
        style={{ width: '100%', height: 280, border: '1px solid #E4E0D6', borderRadius: 8 }}
      />
    </div>
  )
}

export function MonitoringTab({ serviceName }: { serviceName: string }) {
  const baseUrl = getGrafanaBaseUrl()
  const [from, setFrom] = useState<string>(DEFAULT_TIME_RANGE_FROM)

  return (
    <div>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        title="Service dashboard"
        description={
          <>
            These panels show only <strong className="agenda-mono">{serviceName}</strong>. Requires the observability
            add-on (see <code className="agenda-mono">deploy/observability/README.md</code>) to be running.
          </>
        }
      />

      <Space style={{ marginBottom: 16 }}>
        <Typography.Text type="secondary">Time range</Typography.Text>
        <Select
          size="small"
          style={{ width: 120 }}
          value={from}
          onChange={setFrom}
          options={TIME_RANGE_OPTIONS.map((o) => ({ label: o.label, value: o.from }))}
        />
      </Space>

      <Typography.Title level={5} style={{ marginTop: 0 }}>By route</Typography.Title>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 24 }}>
        {ROUTE_PANELS.map((p) => (
          <Panel key={p.id} baseUrl={baseUrl} id={p.id} title={p.title} from={from} serviceName={serviceName} />
        ))}
      </div>

      <Typography.Title level={5}>By endpoint</Typography.Title>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
        <Panel baseUrl={baseUrl} id={ENDPOINT_PANELS[0].id} title={ENDPOINT_PANELS[0].title} from={from} serviceName={serviceName} />
        <Panel baseUrl={baseUrl} id={ENDPOINT_PANELS[1].id} title={ENDPOINT_PANELS[1].title} from={from} serviceName={serviceName} />
        <Panel baseUrl={baseUrl} id={ENDPOINT_PANELS[2].id} title={ENDPOINT_PANELS[2].title} from={from} serviceName={serviceName} full />
      </div>
    </div>
  )
}
