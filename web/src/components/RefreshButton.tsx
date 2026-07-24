import { useState } from 'react'
import { Button, Tooltip } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { useQueryClient, type QueryKey } from '@tanstack/react-query'

// RefreshButton refetches a specific set of query keys in place, so a tab can
// pull fresh data without a full-page reload (which resets the active tab back
// to Instances). Spins while the refetch is in flight.
export function RefreshButton({
  queryKeys,
  tooltip = 'Refresh',
  size,
}: {
  queryKeys: QueryKey[]
  tooltip?: string
  size?: 'small' | 'middle' | 'large'
}) {
  const queryClient = useQueryClient()
  const [loading, setLoading] = useState(false)

  const onClick = async () => {
    setLoading(true)
    try {
      await Promise.all(queryKeys.map((key) => queryClient.refetchQueries({ queryKey: key })))
    } finally {
      setLoading(false)
    }
  }

  return (
    <Tooltip title={tooltip}>
      <Button icon={<ReloadOutlined />} loading={loading} onClick={onClick} size={size} />
    </Tooltip>
  )
}
