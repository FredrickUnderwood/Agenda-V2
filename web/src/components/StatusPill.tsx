export type PillTone = 'verified' | 'building' | 'failed' | 'idle'

const TONE_BY_STATUS: Record<string, PillTone> = {
  verified: 'verified',
  success: 'verified',
  healthy: 'verified',
  online: 'verified',
  deploying: 'building',
  pending_verify: 'building',
  running: 'building',
  draft: 'idle',
  failed: 'failed',
  unhealthy: 'failed',
  offline: 'idle',
  rolling_back: 'building',
  rolled_back: 'idle',
  unknown: 'idle',
}

export function toneForStatus(status: string): PillTone {
  return TONE_BY_STATUS[status] ?? 'idle'
}

export function StatusPill({ status, label }: { status: string; label?: string }) {
  const tone = toneForStatus(status)
  return (
    <span className={`agenda-pill agenda-pill-${tone}`}>
      <span className="agenda-pill-dot" />
      {label ?? status.replace(/_/g, ' ')}
    </span>
  )
}
