import { useState } from 'react'
import { Alert, Button, Select, Space, Tooltip, Typography } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { DEFAULT_TIME_RANGE_FROM, getGrafanaBaseUrl, panelEmbedUrl, TIME_RANGE_OPTIONS } from '@/utils/grafana'

// panelId → title, matching deploy/observability/grafana/dashboards/gateway-overview.json.
// 1/2 are route-level; 3/4/5 are per-endpoint (normalized app-relative path).
const ROUTE_PANELS = [
  { id: 1, title: 'Error rate (5xx) by route' },
  { id: 2, title: 'P99 latency by route' },
] as const

const ENDPOINT_RATE_PANELS = [
  { id: 3, title: 'QPS by endpoint' },
  { id: 4, title: 'Error rate (5xx) by endpoint' },
] as const

const ENDPOINT_LATENCY_PANELS = [
  { id: 5, title: 'P50 latency by endpoint' },
  { id: 6, title: 'P95 latency by endpoint' },
  { id: 7, title: 'P99 latency by endpoint' },
] as const

// Panels driven by the app's own sdk/go/metric instrumentation (real
// registered routes, not the gateway's heuristic path归一). Only populated for
// apps that add the metric middleware; empty otherwise.
const APP_ROUTE_RATE_PANELS = [
  { id: 8, title: 'QPS by app route' },
  { id: 9, title: 'Error rate (5xx) by app route' },
] as const

const APP_ROUTE_LATENCY_PANELS = [
  { id: 10, title: 'P50 latency by app route' },
  { id: 11, title: 'P95 latency by app route' },
  { id: 12, title: 'P99 latency by app route' },
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
  // Bumping the nonce remounts every iframe, forcing Grafana to re-render the
  // panels — the tab has no react-query data of its own to refetch.
  const [nonce, setNonce] = useState(0)

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
        <Tooltip title="Reload panels">
          <Button size="small" icon={<ReloadOutlined />} onClick={() => setNonce((n) => n + 1)} />
        </Tooltip>
      </Space>

      <Typography.Title level={5} style={{ marginTop: 0 }}>By route</Typography.Title>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 24 }}>
        {ROUTE_PANELS.map((p) => (
          <Panel key={`${p.id}-${nonce}`} baseUrl={baseUrl} id={p.id} title={p.title} from={from} serviceName={serviceName} />
        ))}
      </div>

      <Typography.Title level={5}>By endpoint</Typography.Title>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 16 }}>
        {ENDPOINT_RATE_PANELS.map((p) => (
          <Panel key={`${p.id}-${nonce}`} baseUrl={baseUrl} id={p.id} title={p.title} from={from} serviceName={serviceName} />
        ))}
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 16 }}>
        {ENDPOINT_LATENCY_PANELS.map((p) => (
          <Panel key={`${p.id}-${nonce}`} baseUrl={baseUrl} id={p.id} title={p.title} from={from} serviceName={serviceName} />
        ))}
      </div>

      <Typography.Title level={5}>By app route (SDK)</Typography.Title>
      <Typography.Paragraph type="secondary" style={{ marginTop: -8 }}>
        Real registered routes reported by the app's own <code className="agenda-mono">sdk/go/metric</code> middleware.
        Empty for apps that haven't added it.
      </Typography.Paragraph>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 16 }}>
        {APP_ROUTE_RATE_PANELS.map((p) => (
          <Panel key={`${p.id}-${nonce}`} baseUrl={baseUrl} id={p.id} title={p.title} from={from} serviceName={serviceName} />
        ))}
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 16 }}>
        {APP_ROUTE_LATENCY_PANELS.map((p) => (
          <Panel key={`${p.id}-${nonce}`} baseUrl={baseUrl} id={p.id} title={p.title} from={from} serviceName={serviceName} />
        ))}
      </div>
    </div>
  )
}
