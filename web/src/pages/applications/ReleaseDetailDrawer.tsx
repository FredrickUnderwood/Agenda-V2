import { useMemo } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Drawer, Space, Tag, Typography } from 'antd'
import * as api from '@/api/releases'
import type { ReleaseStatus } from '@/api/types'
import { PipelineRail } from '@/components/PipelineRail'
import type { RailStep, RailStepStatus } from '@/components/PipelineRail'
import { StatusPill } from '@/components/StatusPill'
import { errorMessage } from '@/utils/errorMessage'

const STEP_STATUS_MAP: Record<string, RailStepStatus> = {
  pending: 'pending',
  running: 'running',
  success: 'success',
  failed: 'failed',
  skipped: 'skipped',
}

// A release is "live" (polling worth doing) while its pipeline could still be
// moving — deploying, or awaiting a human to click verify.
function isInFlight(status: ReleaseStatus): boolean {
  return status === 'deploying' || status === 'rolling_back'
}

export function ReleaseDetailDrawer({ releaseId, onClose }: { releaseId: number | null; onClose: () => void }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()

  const { data } = useQuery({
    queryKey: ['releases', releaseId],
    queryFn: () => api.getRelease(releaseId!),
    enabled: releaseId != null,
    refetchInterval: (query) => (query.state.data && isInFlight(query.state.data.release.status) ? 2000 : false),
  })

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['releases', releaseId] })
    queryClient.invalidateQueries({ queryKey: ['releases'] })
  }

  const deployMutation = useMutation({
    mutationFn: () => api.deployRelease(releaseId!),
    onSuccess: invalidate,
    onError: (err: unknown) => message.error(errorMessage(err)),
  })
  const verifyMutation = useMutation({
    mutationFn: () => api.verifyRelease(releaseId!),
    onSuccess: () => {
      message.success('Release verified.')
      invalidate()
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })
  const retryMutation = useMutation({
    mutationFn: () => api.retryRelease(releaseId!, 0),
    onSuccess: invalidate,
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const steps: RailStep[] = useMemo(() => {
    const pSteps = data?.deploy_log?.steps
    if (!pSteps?.length) return []
    return pSteps.map((s) => ({ key: String(s.id), label: s.name, status: STEP_STATUS_MAP[s.status] ?? 'pending' }))
  }, [data])

  const release = data?.release

  return (
    <Drawer title={release ? `Release #${release.id}` : 'Release'} open={releaseId != null} onClose={onClose} size={520}>
      {release && (
        <Space direction="vertical" size={20} style={{ width: '100%' }}>
          <div>
            <Space size={8} wrap>
              <Tag>{release.env}</Tag>
              <span className="agenda-mono">{release.instance_name}</span>
              <StatusPill status={release.status} />
            </Space>
            <div style={{ marginTop: 10 }}>
              <Typography.Text type="secondary">Branch </Typography.Text>
              <span className="agenda-mono">{release.branch}</span>
              {release.commit_sha && (
                <>
                  <Typography.Text type="secondary"> @ </Typography.Text>
                  <span className="agenda-mono">{release.commit_sha.slice(0, 10)}</span>
                </>
              )}
            </div>
          </div>

          {steps.length > 0 && (
            <div>
              <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 12 }}>
                Pipeline
              </Typography.Text>
              <PipelineRail steps={steps} />
            </div>
          )}

          {data?.deploy_log && (
            <div className="agenda-terminal" style={{ padding: 12, maxHeight: 320, overflow: 'auto', whiteSpace: 'pre-wrap' }}>
              {data.deploy_log.output || data.deploy_log.error_msg || 'No output yet.'}
            </div>
          )}

          <Space wrap>
            {(release.status === 'draft' || release.status === 'failed') && (
              <Button type="primary" loading={deployMutation.isPending} onClick={() => deployMutation.mutate()}>
                Deploy
              </Button>
            )}
            {release.status === 'failed' && (
              <Button loading={retryMutation.isPending} onClick={() => retryMutation.mutate()}>
                Retry
              </Button>
            )}
            {release.status === 'pending_verify' && (
              <Button type="primary" loading={verifyMutation.isPending} onClick={() => verifyMutation.mutate()}>
                Mark verified
              </Button>
            )}
          </Space>
        </Space>
      )}
    </Drawer>
  )
}
