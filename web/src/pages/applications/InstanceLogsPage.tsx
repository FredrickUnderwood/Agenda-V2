import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Breadcrumb, Button, Empty, Select, Space, Switch, Typography } from 'antd'
import { useNavigate, useParams } from 'react-router-dom'
import * as api from '@/api/applications'

const TAIL_OPTIONS = [100, 200, 500, 1000]

export function InstanceLogsPage() {
  const { appId, targetId } = useParams<{ appId: string; targetId: string }>()
  const navigate = useNavigate()
  const appIdNum = Number(appId)
  const targetIdNum = Number(targetId)

  const [service, setService] = useState<string | undefined>(undefined)
  const [tail, setTail] = useState(200)
  const [live, setLive] = useState(true)

  const { data: app } = useQuery({ queryKey: ['applications', appIdNum], queryFn: () => api.getApplication(appIdNum) })
  const {
    data: logs,
    isLoading,
    isError,
    error,
    refetch,
    isFetching,
  } = useQuery({
    queryKey: ['applications', appIdNum, 'instances', targetIdNum, 'logs', service, tail],
    queryFn: () => api.getInstanceLogs(appIdNum, targetIdNum, { service, tail }),
    enabled: !Number.isNaN(appIdNum) && !Number.isNaN(targetIdNum),
    refetchInterval: live ? 5000 : false,
    retry: false,
  })

  const services = logs?.logs.map((l) => l.service).filter(Boolean) ?? []

  return (
    <div>
      <Breadcrumb
        items={[
          { title: <a onClick={() => navigate('/applications')}>Applications</a> },
          { title: <a onClick={() => navigate(`/applications/${appIdNum}`)}>{app?.name ?? appIdNum}</a> },
          { title: 'Logs' },
        ]}
        style={{ marginBottom: 12 }}
      />

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Typography.Title level={4} className="agenda-display" style={{ margin: 0 }}>
          Instance logs
        </Typography.Title>
        <Space>
          {services.length > 1 && (
            <Select
              placeholder="All services"
              allowClear
              style={{ width: 160 }}
              value={service}
              onChange={setService}
              options={services.map((s) => ({ value: s, label: s }))}
            />
          )}
          <Select value={tail} onChange={setTail} options={TAIL_OPTIONS.map((n) => ({ value: n, label: `Last ${n} lines` }))} />
          <Space size={4}>
            <Switch size="small" checked={live} onChange={setLive} />
            <Typography.Text type="secondary">Live</Typography.Text>
          </Space>
          <Button loading={isFetching} onClick={() => refetch()}>
            Refresh
          </Button>
        </Space>
      </div>

      {isLoading && <div className="agenda-terminal" style={{ padding: 16 }}>Loading…</div>}

      {isError && (
        <Empty
          description={
            <>
              <div>Couldn't reach this instance's logs.</div>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {(error as { response?: { data?: { error?: string } } })?.response?.data?.error ??
                  'The machine may be offline, or this instance has no verified release yet.'}
              </Typography.Text>
            </>
          }
          style={{ marginTop: 48 }}
        />
      )}

      {logs?.logs.map((file) => (
        <div key={file.file} style={{ marginBottom: 16 }}>
          {file.service && (
            <Typography.Text type="secondary" className="agenda-mono" style={{ fontSize: 12, display: 'block', marginBottom: 6 }}>
              {file.service}
            </Typography.Text>
          )}
          <div className="agenda-terminal" style={{ padding: 12, maxHeight: 520, overflow: 'auto' }}>
            {file.lines.length === 0 ? (
              <span style={{ opacity: 0.6 }}>No log lines yet — this instance hasn't written anything.</span>
            ) : (
              // Newest first: the node tails lines oldest→newest; reverse a copy
              // so the most recent line renders at the top (source array untouched).
              file.lines.slice().reverse().map((line, i) => (
                <div key={i} style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                  {line}
                </div>
              ))
            )}
          </div>
        </div>
      ))}
    </div>
  )
}
