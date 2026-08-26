import { Table, Tag, Typography } from 'antd'
import type { DatabaseCell, DatabaseColumn } from '@/api/types'

// A result grid is built from columns the server names at runtime, so it has to
// be assembled rather than declared. Cells are keyed by ordinal, not by column
// name: a query may legitimately return the same name twice ("SELECT a.id,
// b.id"), and keying by name would silently drop one of them.
export function ResultGrid({
  columns,
  rows,
  height = 420,
}: {
  columns: DatabaseColumn[]
  rows: DatabaseCell[][]
  height?: number
}) {
  const dataSource = rows.map((row, index) => {
    const record: Record<string, DatabaseCell | number> = { __key: index }
    row.forEach((cell, i) => {
      record[`c${i}`] = cell
    })
    return record
  })

  return (
    <Table
      size="small"
      rowKey="__key"
      dataSource={dataSource}
      pagination={false}
      scroll={{ x: 'max-content', y: height }}
      columns={columns.map((col, i) => ({
        title: (
          <span>
            {col.name}
            <Typography.Text type="secondary" style={{ marginLeft: 6, fontWeight: 400, fontSize: 11 }}>
              {col.type}
            </Typography.Text>
          </span>
        ),
        dataIndex: `c${i}`,
        key: `c${i}`,
        ellipsis: true,
        render: (value: DatabaseCell) => <Cell value={value} binary={col.binary} />,
      }))}
    />
  )
}

function Cell({ value, binary }: { value: DatabaseCell; binary: boolean }) {
  // NULL and the empty string are different answers, so they must not look the
  // same in the grid.
  if (value === null) {
    return (
      <Typography.Text type="secondary" italic style={{ fontSize: 12 }}>
        NULL
      </Typography.Text>
    )
  }
  if (binary) {
    return (
      <span className="agenda-mono" style={{ fontSize: 12 }}>
        <Tag style={{ marginRight: 6 }}>base64</Tag>
        {value}
      </span>
    )
  }
  return (
    <span className="agenda-mono" style={{ fontSize: 12 }}>
      {value}
    </span>
  )
}
