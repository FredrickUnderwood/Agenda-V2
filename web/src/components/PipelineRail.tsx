import { color } from '@/theme/tokens'

export type RailStepStatus = 'pending' | 'running' | 'success' | 'failed' | 'skipped'

export interface RailStep {
  key: string
  label: string
  status: RailStepStatus
}

const DOT_COLOR: Record<RailStepStatus, string> = {
  pending: color.ink500,
  running: color.signal,
  success: color.verified,
  failed: color.fail,
  skipped: color.ink500,
}

// The signature element: a horizontal sequence of connected step-nodes. Used
// for anything that's a genuine ordered sequence — a deploy pipeline's
// actual steps, a release history timeline — never as decorative numbering.
export function PipelineRail({ steps }: { steps: RailStep[] }) {
  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', width: '100%' }}>
      {steps.map((step, i) => {
        const isLast = i === steps.length - 1
        const lineDone = step.status === 'success'
        return (
          <div
            key={step.key}
            style={{
              display: 'flex',
              alignItems: 'center',
              flex: isLast ? '0 0 auto' : '1 1 0',
              minWidth: 0,
            }}
          >
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 6, flex: '0 0 auto' }}>
              <span
                aria-label={`${step.label}: ${step.status}`}
                style={{
                  width: 12,
                  height: 12,
                  borderRadius: 999,
                  background: step.status === 'pending' ? 'transparent' : DOT_COLOR[step.status],
                  border: `2px solid ${DOT_COLOR[step.status]}`,
                  animation: step.status === 'running' ? 'agenda-pulse 1.4s ease-in-out infinite' : undefined,
                }}
              />
              <span
                className="agenda-mono"
                style={{ fontSize: 11, color: color.ink500, whiteSpace: 'nowrap', maxWidth: 96, textAlign: 'center' }}
                title={step.label}
              >
                {step.label}
              </span>
            </div>
            {!isLast && (
              <div
                style={{
                  height: 2,
                  flex: '1 1 auto',
                  marginTop: -18,
                  background: lineDone ? color.verified : color.paperBorder,
                  minWidth: 16,
                }}
              />
            )}
          </div>
        )
      })}
    </div>
  )
}
