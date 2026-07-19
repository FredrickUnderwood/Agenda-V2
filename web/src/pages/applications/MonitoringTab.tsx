import { useState } from 'react'
import { Alert, Select, Space, Typography } from 'antd'
import { DEFAULT_TIME_RANGE_FROM, getGrafanaBaseUrl, panelEmbedUrl, TIME_RANGE_OPTIONS } from '@/utils/grafana'

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

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
        <div>
          <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 8 }}>
            Error rate (5xx)
          </Typography.Text>
          <iframe
            title="Error rate"
            src={panelEmbedUrl(baseUrl, 1, from, serviceName)}
            style={{ width: '100%', height: 280, border: '1px solid #E4E0D6', borderRadius: 8 }}
          />
        </div>
        <div>
          <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 8 }}>
            P99 latency
          </Typography.Text>
          <iframe
            title="P99 latency"
            src={panelEmbedUrl(baseUrl, 2, from, serviceName)}
            style={{ width: '100%', height: 280, border: '1px solid #E4E0D6', borderRadius: 8 }}
          />
        </div>
      </div>
    </div>
  )
}
