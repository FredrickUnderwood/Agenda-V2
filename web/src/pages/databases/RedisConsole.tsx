import { useMemo, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Alert, Button, Card, Empty, Input, InputNumber, Select, Space, Tag, Typography } from 'antd'
import { PlayCircleOutlined } from '@ant-design/icons'
import * as api from '@/api/databases'
import type { DatabaseInstance, QueryResult } from '@/api/types'
import { errorMessage } from '@/utils/errorMessage'
import { ResultGrid } from './resultGrid'

// A few commands worth having one click away. This is a starting point, not the
// allowlist — the server holds that, and duplicating it here would leave two
// lists to keep in step.
const EXAMPLES = ['GET key', 'TTL key', 'TYPE key', 'SCAN 0 MATCH prefix:* COUNT 100', 'HGETALL key', 'LRANGE key 0 -1', 'INFO server']

export function RedisConsole({ instances, loading }: { instances: DatabaseInstance[]; loading: boolean }) {
  const runnable = useMemo(() => instances.filter((i) => i.enabled && i.engine === 'redis'), [instances])
  const [instanceId, setInstanceId] = useState<number | undefined>()
  const [db, setDb] = useState(0)
  const [command, setCommand] = useState('')
  const [maxRows, setMaxRows] = useState(1000)
  const [result, setResult] = useState<QueryResult | null>(null)
  const [error, setError] = useState<string | null>(null)

  const instance = runnable.find((i) => i.id === instanceId)

  // How many databases this server has. The server falls back to Redis's own
  // default when the account may not read the setting, so this never fails the
  // picker outright.
  const databases = useQuery({
    queryKey: ['redis-databases', instanceId],
    queryFn: () => api.getRedisDatabaseCount(instanceId!),
    enabled: !!instanceId,
    retry: false,
  })
  const dbCount = databases.data?.count ?? 16

  const runMutation = useMutation({
    mutationFn: () => api.runRedisCommand(instanceId!, { db, command, max_rows: maxRows }),
    onSuccess: (res) => {
      setResult(res)
      setError(null)
    },
    onError: (err: unknown) => {
      setResult(null)
      setError(errorMessage(err))
    },
  })

  const canRun = !!instanceId && command.trim().length > 0 && !runMutation.isPending

  function selectInstance(id: number) {
    setInstanceId(id)
    const next = runnable.find((i) => i.id === id)
    // The registered default is stored as text, since the same column holds a
    // schema name for MySQL.
    const registered = Number(next?.default_database)
    setDb(Number.isInteger(registered) && registered >= 0 ? registered : 0)
    setResult(null)
    setError(null)
  }

  if (!loading && runnable.length === 0) {
    return (
      <Empty
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description="No Redis instance is registered yet. Add one under Instances, with Engine set to Redis."
      />
    )
  }

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Space wrap>
        <Select
          style={{ minWidth: 240 }}
          placeholder="Redis instance"
          loading={loading}
          value={instanceId}
          onChange={selectInstance}
          options={runnable.map((i) => ({
            value: i.id,
            label: (
              <span>
                {i.name} <Tag color={i.env === 'prod' ? 'red' : i.env === 'stage' ? 'orange' : 'blue'}>{i.env}</Tag>
              </span>
            ),
          }))}
        />
        <Select
          style={{ minWidth: 120 }}
          value={db}
          onChange={setDb}
          disabled={!instanceId}
          loading={databases.isFetching}
          showSearch
          options={Array.from({ length: dbCount }, (_, i) => ({ value: i, label: `db${i}` }))}
        />
        <Space size={4}>
          <Typography.Text type="secondary">Max rows</Typography.Text>
          <InputNumber min={1} max={10000} value={maxRows} onChange={(v) => setMaxRows(v ?? 1000)} style={{ width: 96 }} />
        </Space>
        <Select
          style={{ minWidth: 220 }}
          placeholder="Insert an example"
          value={null}
          options={EXAMPLES.map((c) => ({ value: c, label: c }))}
          onChange={(c: string) => setCommand(c)}
        />
      </Space>

      {instance?.env === 'prod' && (
        <Alert
          type="warning"
          showIcon
          message="This is a production Redis. Every command you run is recorded in the query history."
        />
      )}

      <Input
        className="agenda-mono"
        size="large"
        placeholder="GET user:1"
        value={command}
        onChange={(e) => setCommand(e.target.value)}
        onPressEnter={() => {
          if (canRun) runMutation.mutate()
        }}
      />

      <Space>
        <Button
          type="primary"
          icon={<PlayCircleOutlined />}
          disabled={!canRun}
          loading={runMutation.isPending}
          onClick={() => runMutation.mutate()}
        >
          Run
        </Button>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          Enter · read-only commands only (GET, MGET, SCAN, TTL, TYPE, H*/L*/S*/Z* reads, INFO, CONFIG GET …)
        </Typography.Text>
      </Space>

      {error && <Alert type="error" message={error} />}

      {result && (
        <Card size="small">
          <Space direction="vertical" size={10} style={{ width: '100%' }}>
            <Space split="·" wrap>
              <Typography.Text strong>{result.row_count} rows</Typography.Text>
              <Typography.Text type="secondary">{result.duration_ms} ms</Typography.Text>
              {result.database && <Typography.Text type="secondary">{result.database}</Typography.Text>}
              {result.truncated && <Tag color="orange">Truncated at {maxRows} rows</Tag>}
            </Space>
            {result.row_count === 0 ? (
              <Empty description="The command returned nothing." image={Empty.PRESENTED_IMAGE_SIMPLE} />
            ) : (
              <ResultGrid columns={result.columns} rows={result.rows} />
            )}
          </Space>
        </Card>
      )}
    </Space>
  )
}
