import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Button, Drawer, Modal, Select, Space, Tag, Typography } from 'antd'
import * as api from '@/api/releases'
import type { ApplicationRelease, ReleaseStatus } from '@/api/types'
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

// Rolling back means "put the previous version back on this instance", so it
// only makes sense once this release actually reached the instance. A failed
// release never replaced anything, and one already rolling back or rolled back
// has been dealt with.
function canRollBack(status: ReleaseStatus): boolean {
  return status === 'pending_verify' || status === 'verified'
}

// AUTO_TARGET is the "let the server pick" sentinel the rollback API already
// uses: target_release_id 0 means "the last verified release before this one".
const AUTO_TARGET = 0

export function ReleaseDetailDrawer({
  releaseId,
  onClose,
  onNavigate,
}: {
  releaseId: number | null
  onClose: () => void
  // Called with the replacement release after a rollback, so the drawer follows
  // the pipeline the operator just started instead of sitting on the release it
  // superseded.
  onNavigate?: (releaseId: number) => void
}) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [rollbackOpen, setRollbackOpen] = useState(false)
  const [rollbackTarget, setRollbackTarget] = useState<number>(AUTO_TARGET)

  const { data } = useQuery({
    queryKey: ['releases', releaseId],
    queryFn: () => api.getRelease(releaseId!),
    enabled: releaseId != null,
    refetchInterval: (query) => (query.state.data && isInFlight(query.state.data.release.status) ? 2000 : false),
  })

  const release = data?.release

  // Candidate rollback targets: this instance's own verified history. Loaded
  // only while the picker is open — most visits to this drawer never roll back.
  const { data: candidates, isLoading: candidatesLoading } = useQuery({
    queryKey: ['releases', 'rollbackTargets', release?.id],
    queryFn: () =>
      api.listReleases(release!.application_id, {
        env: release!.env,
        instance_name: release!.instance_name,
        status: 'verified',
        limit: 20,
      }),
    enabled: rollbackOpen && release != null,
  })

  // Only releases older than this one: redeploying a *newer* verified release
  // is a deploy, not a rollback, and the automatic target is defined the same
  // way server-side.
  const targetOptions = useMemo(() => {
    const rows = (candidates?.data ?? []).filter((r: ApplicationRelease) => release != null && r.id < release.id)
    return rows.map((r: ApplicationRelease) => ({
      value: r.id,
      label: `#${r.id} · ${r.branch} @ ${r.commit_sha ? r.commit_sha.slice(0, 10) : '—'} · ${new Date(r.created_at).toLocaleString()}`,
    }))
  }, [candidates, release])

  useEffect(() => {
    if (!rollbackOpen) setRollbackTarget(AUTO_TARGET)
  }, [rollbackOpen])

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['releases', releaseId] })
    queryClient.invalidateQueries({ queryKey: ['releases'] })
    queryClient.invalidateQueries({ queryKey: ['env-deployments'] })
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
  const rollbackMutation = useMutation({
    mutationFn: () => api.rollbackRelease(releaseId!, rollbackTarget),
    onSuccess: (created) => {
      message.success(`Rolling back to ${created.commit_sha ? created.commit_sha.slice(0, 10) : created.branch}.`)
      setRollbackOpen(false)
      invalidate()
      onNavigate?.(created.id)
    },
    onError: (err: unknown) => message.error(errorMessage(err)),
  })

  const steps: RailStep[] = useMemo(() => {
    const pSteps = data?.deploy_log?.steps
    if (!pSteps?.length) return []
    return pSteps.map((s) => ({ key: String(s.id), label: s.name, status: STEP_STATUS_MAP[s.status] ?? 'pending' }))
  }, [data])

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
            {release.previous_release_id > 0 && (
              <div style={{ marginTop: 6 }}>
                <Typography.Text type="secondary">
                  Rollback of release #{release.previous_release_id}
                </Typography.Text>
              </div>
            )}
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
            {canRollBack(release.status) && (
              <Button danger onClick={() => setRollbackOpen(true)}>
                Roll back
              </Button>
            )}
          </Space>
        </Space>
      )}

      <Modal
        title={`Roll back ${release?.instance_name ?? ''}`}
        open={rollbackOpen}
        onCancel={() => setRollbackOpen(false)}
        onOk={() => rollbackMutation.mutate()}
        confirmLoading={rollbackMutation.isPending}
        okText="Roll back"
        okButtonProps={{ danger: true }}
      >
        <p style={{ marginTop: 0, color: 'var(--agenda-ink-500, #888)' }}>
          Redeploys this instance from an earlier verified release. Only the <strong>commit</strong> is rolled back —
          environment variables, deploy configuration and gateway routes stay as they are now.
        </p>
        <Select
          style={{ width: '100%' }}
          value={rollbackTarget}
          onChange={setRollbackTarget}
          loading={candidatesLoading}
          options={[
            { value: AUTO_TARGET, label: 'Previous verified release (automatic)' },
            ...targetOptions,
          ]}
        />
      </Modal>
    </Drawer>
  )
}
