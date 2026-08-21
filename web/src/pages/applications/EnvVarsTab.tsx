import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, App, Button, Empty, Input, Space, Table, Tooltip, Typography } from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import * as api from '@/api/applications'
import type { EnvVarMatrix, Environment } from '@/api/types'
import { RefreshButton } from '@/components/RefreshButton'
import { errorMessage } from '@/utils/errorMessage'

const ENVS: Environment[] = ['prod', 'stage', 'test']
const ENV_LABEL: Record<Environment, string> = { prod: 'Prod', stage: 'Stage', test: 'Test' }

// Mirrors the backend's domain.ValidEnvVarKey. Checking here too turns a
// rejected save into an inline hint on the offending cell.
const KEY_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/
const RESERVED_PREFIX = 'AGENDA_'

// One row of the matrix: a variable name and its value in each environment.
// Rows carry a client-side id because the key is editable (and may be blank
// while being typed), so it can't serve as the table's row key.
interface EnvVarRow {
  id: string
  key: string
  values: Record<Environment, string>
}

let rowSeq = 0
function newRow(): EnvVarRow {
  rowSeq += 1
  return { id: `row-${rowSeq}`, key: '', values: { prod: '', stage: '', test: '' } }
}

// The matrix is the union of keys across environments: a variable set in only
// one environment still gets a row, with the other cells blank.
function rowsFromMatrix(envs: EnvVarMatrix | undefined): EnvVarRow[] {
  if (!envs) return []
  const keys = new Set<string>()
  for (const env of ENVS) for (const k of Object.keys(envs[env] ?? {})) keys.add(k)
  return [...keys].sort().map((key) => {
    const row = newRow()
    row.key = key
    for (const env of ENVS) row.values[env] = envs[env]?.[key] ?? ''
    return row
  })
}

// A blank cell is saved as an empty string, not as "inherit from prod" — there
// is no inheritance between environments, so what the matrix shows is exactly
// what the container gets. Rows with a blank key are dropped.
function matrixFromRows(rows: EnvVarRow[]): EnvVarMatrix {
  const out = { prod: {}, stage: {}, test: {} } as EnvVarMatrix
  for (const row of rows) {
    const key = row.key.trim()
    if (!key) continue
    for (const env of ENVS) out[env][key] = row.values[env] ?? ''
  }
  return out
}

function keyError(key: string, rows: EnvVarRow[]): string | null {
  const k = key.trim()
  if (!k) return null
  if (k.startsWith(RESERVED_PREFIX)) return `${RESERVED_PREFIX}* names are reserved by the platform.`
  if (!KEY_PATTERN.test(k)) return 'Use letters, digits and underscore; may not start with a digit.'
  if (rows.filter((r) => r.key.trim() === k).length > 1) return 'Duplicate variable name.'
  return null
}

export function EnvVarsTab({ appId }: { appId: number }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const queryKey = ['applications', appId, 'environments']

  const { data, isLoading } = useQuery({
    queryKey,
    queryFn: () => api.getApplicationEnvironments(appId),
  })

  const [rows, setRows] = useState<EnvVarRow[]>([])
  // Snapshot of what the server has, to detect unsaved edits. Refetches reset
  // the editor, which is safe because a save invalidates the query itself.
  const [baseline, setBaseline] = useState('')

  useEffect(() => {
    const next = rowsFromMatrix(data?.envs)
    setRows(next)
    setBaseline(JSON.stringify(matrixFromRows(next)))
  }, [data])

  const dirty = useMemo(() => JSON.stringify(matrixFromRows(rows)) !== baseline, [rows, baseline])
  const errors = useMemo(
    () => rows.map((r) => keyError(r.key, rows)).filter((e): e is string => e !== null),
    [rows],
  )

  const saveMutation = useMutation({
    mutationFn: () => api.updateApplicationEnvironments(appId, { envs: matrixFromRows(rows) }),
    onSuccess: () => {
      message.success('Environment variables saved. They apply on the next deploy.')
      queryClient.invalidateQueries({ queryKey })
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  function update(id: string, patch: (row: EnvVarRow) => EnvVarRow) {
    setRows((prev) => prev.map((r) => (r.id === id ? patch(r) : r)))
  }

  const columns = [
    {
      title: 'Key',
      dataIndex: 'key',
      width: 240,
      render: (_: unknown, row: EnvVarRow) => {
        const err = keyError(row.key, rows)
        return (
          <Tooltip title={err} open={err ? undefined : false}>
            <Input
              value={row.key}
              status={err ? 'error' : undefined}
              placeholder="DB_DSN"
              className="agenda-mono"
              onChange={(e) => update(row.id, (r) => ({ ...r, key: e.target.value }))}
            />
          </Tooltip>
        )
      },
    },
    ...ENVS.map((env) => ({
      title: ENV_LABEL[env],
      dataIndex: env,
      render: (_: unknown, row: EnvVarRow) => (
        <Input
          value={row.values[env]}
          placeholder="(empty)"
          className="agenda-mono"
          onChange={(e) =>
            update(row.id, (r) => ({ ...r, values: { ...r.values, [env]: e.target.value } }))
          }
        />
      ),
    })),
    {
      title: '',
      dataIndex: 'actions',
      width: 48,
      render: (_: unknown, row: EnvVarRow) => (
        <Tooltip title="Remove variable">
          <Button
            type="text"
            icon={<DeleteOutlined />}
            onClick={() => setRows((prev) => prev.filter((r) => r.id !== row.id))}
          />
        </Tooltip>
      ),
    },
  ]

  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Space style={{ justifyContent: 'space-between', width: '100%' }} align="start">
        <Typography.Text type="secondary">
          Injected into every container of the environment on deploy. A blank cell is deployed as an
          empty value — environments do not inherit from each other. Instance-level overrides still
          win over these.
        </Typography.Text>
        <Space>
          <RefreshButton queryKeys={[queryKey]} />
          <Button icon={<PlusOutlined />} onClick={() => setRows((prev) => [...prev, newRow()])}>
            Add variable
          </Button>
        </Space>
      </Space>

      {errors.length > 0 && <Alert type="error" showIcon message={errors[0]} />}

      <Table<EnvVarRow>
        rowKey="id"
        size="small"
        loading={isLoading}
        dataSource={rows}
        columns={columns}
        pagination={false}
        locale={{
          emptyText: (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description="No environment variables yet."
            />
          ),
        }}
      />

      <Space>
        <Button
          type="primary"
          disabled={!dirty || errors.length > 0}
          loading={saveMutation.isPending}
          onClick={() => saveMutation.mutate()}
        >
          Save
        </Button>
        <Button
          disabled={!dirty || saveMutation.isPending}
          onClick={() => setRows(rowsFromMatrix(data?.envs))}
        >
          Discard changes
        </Button>
        {dirty && <Typography.Text type="warning">Unsaved changes</Typography.Text>}
      </Space>
    </Space>
  )
}
