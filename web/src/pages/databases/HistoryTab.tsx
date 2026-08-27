import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Alert, Drawer, Empty, Space, Table, Tag, Typography } from 'antd'
import * as api from '@/api/databases'
import type { DBQueryLog } from '@/api/types'
import { useAuth } from '@/auth/AuthContext'
import { errorMessage } from '@/utils/errorMessage'
import { ResultGrid } from './resultGrid'

const ENV_COLOR: Record<string, string> = { prod: 'red', stage: 'orange', test: 'blue' }

export function HistoryTab() {
  const { user } = useAuth()
  const [openId, setOpenId] = useState<number | null>(null)

  // The server decides what this returns: an admin gets everyone's history, a
  // member only their own. There is no client-side filter to get wrong.
  const logs = useQuery({ queryKey: ['db-query-logs'], queryFn: () => api.listQueryLogs({ limit: 100 }) })

  const detail = useQuery({
    queryKey: ['db-query-log', openId],
    queryFn: () => api.getQueryLog(openId!),
    enabled: openId !== null,
  })

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Typography.Text type="secondary">
        {user?.role === 'admin'
          ? 'Every query run from this console, by anyone. Stored results are removed once they pass the retention window.'
          : 'The queries you have run. Stored results are removed once they pass the retention window.'}
      </Typography.Text>

      <Table<DBQueryLog>
        rowKey="id"
        size="small"
        loading={logs.isLoading}
        dataSource={logs.data?.data ?? []}
        pagination={{ pageSize: 20, showSizeChanger: false }}
        onRow={(row) => ({ onClick: () => setOpenId(row.id), style: { cursor: 'pointer' } })}
        columns={[
          {
            title: 'When',
            dataIndex: 'created_at',
            width: 180,
            render: (v: string) => <span className="agenda-mono">{new Date(v).toLocaleString()}</span>,
          },
          { title: 'Who', dataIndex: 'username', width: 130, render: (v: string) => v || <Typography.Text type="secondary">—</Typography.Text> },
          {
            title: 'Database',
            key: 'target',
            width: 220,
            render: (_, row) => (
              <Space size={6}>
                <span>{row.instance_name}</span>
                <Tag color={ENV_COLOR[row.env]}>{row.env}</Tag>
                {row.database_name && <span className="agenda-mono" style={{ fontSize: 12 }}>{row.database_name}</span>}
              </Space>
            ),
          },
          {
            title: 'Statement',
            dataIndex: 'statement',
            ellipsis: true,
            render: (v: string) => <span className="agenda-mono" style={{ fontSize: 12 }}>{v}</span>,
          },
          {
            title: 'Result',
            key: 'result',
            width: 150,
            render: (_, row) =>
              row.success ? (
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {row.row_count} rows · {row.duration_ms} ms
                </Typography.Text>
              ) : (
                <Tag color="red">failed</Tag>
              ),
          },
        ]}
      />

      <Drawer
        width={880}
        open={openId !== null}
        onClose={() => setOpenId(null)}
        title={detail.data ? `${detail.data.instance_name} · ${new Date(detail.data.created_at).toLocaleString()}` : 'Query'}
        destroyOnHidden
      >
        {detail.isLoading && <Typography.Text type="secondary">Loading…</Typography.Text>}
        {detail.error && <Alert type="error" showIcon message={errorMessage(detail.error)} />}
        {detail.data && (
          <Space direction="vertical" size={16} style={{ width: '100%' }}>
            <div>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                Statement
              </Typography.Text>
              <pre className="agenda-mono" style={{ margin: '6px 0 0', whiteSpace: 'pre-wrap', fontSize: 12 }}>
                {detail.data.statement}
              </pre>
            </div>

            {!detail.data.success && <Alert type="error" showIcon message={detail.data.error || 'The query failed.'} />}

            {detail.data.success && detail.data.result && (
              <Space direction="vertical" size={8} style={{ width: '100%' }}>
                <Space split="·">
                  <Typography.Text strong>{detail.data.row_count} rows</Typography.Text>
                  <Typography.Text type="secondary">{detail.data.duration_ms} ms</Typography.Text>
                  {detail.data.result.truncated && <Tag color="orange">Stored result is an excerpt</Tag>}
                </Space>
                {detail.data.result.rows.length > 0 ? (
                  <ResultGrid columns={detail.data.result.columns} rows={detail.data.result.rows} height={480} />
                ) : (
                  <Empty
                    image={Empty.PRESENTED_IMAGE_SIMPLE}
                    description={
                      detail.data.row_count > 0
                        ? 'The result was too large to keep; re-run the query to see it.'
                        : 'The query returned no rows.'
                    }
                  />
                )}
              </Space>
            )}

            {detail.data.success && !detail.data.result && (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="The stored result is no longer available." />
            )}
          </Space>
        )}
      </Drawer>
    </Space>
  )
}
