import { useMemo, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Alert, Button, Card, Empty, Input, InputNumber, Select, Space, Tag, Typography } from 'antd'
import { PlayCircleOutlined } from '@ant-design/icons'
import * as api from '@/api/databases'
import type { DatabaseInstance, QueryResult } from '@/api/types'
import { errorMessage } from '@/utils/errorMessage'
import { ResultGrid } from './resultGrid'

export function SqlConsole({ instances, loading }: { instances: DatabaseInstance[]; loading: boolean }) {
  const runnable = useMemo(() => instances.filter((i) => i.enabled), [instances])
  const [instanceId, setInstanceId] = useState<number | undefined>()
  const [database, setDatabase] = useState<string | undefined>()
  const [sql, setSql] = useState('')
  const [maxRows, setMaxRows] = useState(1000)
  const [result, setResult] = useState<QueryResult | null>(null)
  const [error, setError] = useState<string | null>(null)

  const instance = runnable.find((i) => i.id === instanceId)

  const databases = useQuery({
    queryKey: ['db-databases', instanceId],
    queryFn: () => api.listDatabases(instanceId!),
    enabled: !!instanceId,
    retry: false,
  })

  const tables = useQuery({
    queryKey: ['db-tables', instanceId, database],
    queryFn: () => api.listTables(instanceId!, database!),
    enabled: !!instanceId && !!database,
    retry: false,
  })

  const runMutation = useMutation({
    mutationFn: () => api.runQuery(instanceId!, { database, sql, max_rows: maxRows }),
    onSuccess: (res) => {
      setResult(res)
      setError(null)
    },
    onError: (err: unknown) => {
      setResult(null)
      setError(errorMessage(err))
    },
  })

  const canRun = !!instanceId && sql.trim().length > 0 && !runMutation.isPending

  function selectInstance(id: number) {
    setInstanceId(id)
    const next = runnable.find((i) => i.id === id)
    setDatabase(next?.default_database || undefined)
    setResult(null)
    setError(null)
  }

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Space wrap>
        <Select
          style={{ minWidth: 240 }}
          placeholder="Database instance"
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
          style={{ minWidth: 200 }}
          placeholder="Schema"
          value={database}
          onChange={(v) => setDatabase(v)}
          loading={databases.isFetching}
          disabled={!instanceId}
          showSearch
          options={(databases.data?.data ?? []).map((d) => ({ value: d, label: d }))}
          notFoundContent={databases.error ? errorMessage(databases.error) : undefined}
        />
        <Select
          style={{ minWidth: 220 }}
          placeholder="Insert a table"
          value={null}
          disabled={!database}
          loading={tables.isFetching}
          showSearch
          options={(tables.data?.data ?? []).map((t) => ({ value: t, label: t }))}
          onChange={(t: string) => setSql(`SELECT * FROM \`${t}\` LIMIT 100`)}
        />
        <Space size={4}>
          <Typography.Text type="secondary">Max rows</Typography.Text>
          <InputNumber min={1} max={10000} value={maxRows} onChange={(v) => setMaxRows(v ?? 1000)} style={{ width: 96 }} />
        </Space>
      </Space>

      {instance?.env === 'prod' && (
        <Alert
          type="warning"
          showIcon
          message="This is a production database. Every statement you run is recorded in the query history."
        />
      )}

      <Input.TextArea
        className="agenda-mono"
        value={sql}
        onChange={(e) => setSql(e.target.value)}
        placeholder="SELECT … — read-only statements only"
        autoSize={{ minRows: 6, maxRows: 16 }}
        spellCheck={false}
        // Running on Cmd/Ctrl+Enter is what anyone arriving from a database
        // client will try first.
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === 'Enter' && canRun) {
            e.preventDefault()
            runMutation.mutate()
          }
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
          {navigator.platform.includes('Mac') ? '⌘' : 'Ctrl'}+Enter · SELECT, WITH, SHOW, DESCRIBE and EXPLAIN only
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
              <Empty description="The query returned no rows." image={Empty.PRESENTED_IMAGE_SIMPLE} />
            ) : (
              <ResultGrid columns={result.columns} rows={result.rows} />
            )}
          </Space>
        </Card>
      )}
    </Space>
  )
}
