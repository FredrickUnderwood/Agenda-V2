import { useState } from 'react'
import { Alert, Input, Space, Typography } from 'antd'
import { getGrafanaBaseUrl, panelEmbedUrl, setGrafanaBaseUrl } from '@/utils/grafana'

export function MonitoringTab({ serviceName }: { serviceName: string }) {
  const [baseUrl, setBaseUrl] = useState(getGrafanaBaseUrl())

  return (
    <div>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        title="Gateway-wide dashboard"
        description={
          <>
            These panels cover every app behind the gateway — find <strong className="agenda-mono">{serviceName}</strong> in
            the legend. Requires the observability add-on (see{' '}
            <code className="agenda-mono">deploy/observability/README.md</code>) to be running.
          </>
        }
      />

      <Space style={{ marginBottom: 16 }}>
        <Typography.Text type="secondary">Grafana URL</Typography.Text>
        <Input
          size="small"
          style={{ width: 260 }}
          value={baseUrl}
          onChange={(e) => setBaseUrl(e.target.value)}
          onBlur={() => setGrafanaBaseUrl(baseUrl)}
        />
      </Space>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
        <div>
          <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 8 }}>
            Error rate (5xx)
          </Typography.Text>
          <iframe
            title="Error rate"
            src={panelEmbedUrl(baseUrl, 1)}
            style={{ width: '100%', height: 280, border: '1px solid #E4E0D6', borderRadius: 8 }}
          />
        </div>
        <div>
          <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 8 }}>
            P99 latency
          </Typography.Text>
          <iframe
            title="P99 latency"
            src={panelEmbedUrl(baseUrl, 2)}
            style={{ width: '100%', height: 280, border: '1px solid #E4E0D6', borderRadius: 8 }}
          />
        </div>
      </div>
    </div>
  )
}
