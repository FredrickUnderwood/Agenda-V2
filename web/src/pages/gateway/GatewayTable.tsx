import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Input, Space, Table, Tag, Tooltip } from 'antd'
import { StatusPill } from '@/components/StatusPill'
import { color } from '@/theme/tokens'
import type { GatewayModel, GwRoute } from './model'
import { healthColor } from './model'

const BACKEND_MODE_LABEL: Record<string, string> = {
  all_enabled: 'all enabled',
  single: 'single',
  selected: 'selected',
}

// Every gateway route, one row per route — the "table mode" of the visualization.
export function GatewayTable({ model }: { model: GatewayModel }) {
  const navigate = useNavigate()
  const [q, setQ] = useState('')

  const rows = useMemo(() => {
    const needle = q.trim().toLowerCase()
    if (!needle) return model.routes
    return model.routes.filter((r) =>
      [r.host || 'any', r.pathPrefix, r.routeKey, r.appName, r.env, ...r.instances.map((i) => i.name)]
        .join(' ')
        .toLowerCase()
        .includes(needle),
    )
  }, [model.routes, q])

  return (
    <div>
      <Input.Search
        allowClear
        placeholder="Filter by host, path, service, instance…"
        onChange={(e) => setQ(e.target.value)}
        style={{ maxWidth: 360, marginBottom: 16 }}
      />
      <Table<GwRoute>
        rowKey="id"
        dataSource={rows}
        pagination={false}
        onRow={(r) => ({ onClick: () => navigate(`/applications/${r.appId}`), style: { cursor: 'pointer' } })}
        columns={[
          {
            title: 'Inbound',
            key: 'inbound',
            render: (_, r) => (
              <span className="agenda-mono" style={{ fontSize: 13 }}>
                <span style={{ color: r.host ? undefined : color.ink500 }}>{r.host || '* any'}</span>
                <span style={{ fontWeight: 600 }}>{r.pathPrefix || '/'}</span>
              </span>
            ),
          },
          { title: 'Route key', dataIndex: 'routeKey', render: (v: string) => <span className="agenda-mono">{v}</span> },
          {
            title: 'Service',
            key: 'service',
            render: (_, r) => (
              <Space size={6}>
                <span className="agenda-display" style={{ fontWeight: 600 }}>
                  {r.appName}
                </span>
                <Tag color={r.env === 'prod' ? 'orange' : undefined} style={{ marginInlineEnd: 0 }}>
                  {r.env}
                </Tag>
              </Space>
            ),
          },
          {
            title: 'Backends',
            key: 'backends',
            render: (_, r) => (
              <Space size={4} wrap>
                <Tag style={{ marginInlineEnd: 4 }}>{BACKEND_MODE_LABEL[r.backendMode] ?? r.backendMode}</Tag>
                {r.instances.length === 0 ? (
                  <span style={{ color: color.fail, fontSize: 12 }}>no instances</span>
                ) : (
                  r.instances.map((i) => (
                    <Tooltip key={i.targetId} title={`${i.health}${i.enabled ? '' : ' · disabled'}${i.weight != null ? ` · weight ${i.weight}` : ''}`}>
                      <span className="agenda-mono" style={{ fontSize: 12, display: 'inline-flex', alignItems: 'center', gap: 4, opacity: i.enabled ? 1 : 0.5 }}>
                        <span style={{ width: 7, height: 7, borderRadius: 999, background: healthColor(i.health) }} />
                        {i.name}
                        {i.weight != null ? <span style={{ color: color.ink500 }}>·w{i.weight}</span> : null}
                      </span>
                    </Tooltip>
                  ))
                )}
              </Space>
            ),
          },
          {
            title: 'Status',
            dataIndex: 'enabled',
            width: 1,
            render: (v: boolean) =>
              v ? <StatusPill status="verified" label="enabled" /> : <StatusPill status="idle" label="disabled" />,
          },
        ]}
        locale={{ emptyText: q ? 'No routes match your filter.' : 'No gateway routes configured yet.' }}
      />
    </div>
  )
}
