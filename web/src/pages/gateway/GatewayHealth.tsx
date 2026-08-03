import { Tooltip } from 'antd'
import { color } from '@/theme/tokens'
import { ENV_STATUS_META, type HealthSummary } from './model'

// A colored status dot + label, sized for either the status bar (lg) or an env
// tab label (sm).
export function StatusDot({ status, label, size = 'sm' }: { status: keyof typeof ENV_STATUS_META; label?: string; size?: 'sm' | 'lg' }) {
  const meta = ENV_STATUS_META[status]
  const d = size === 'lg' ? 10 : 8
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>
      <span style={{ width: d, height: d, borderRadius: 999, background: meta.color, flex: 'none' }} />
      <span
        style={{
          fontWeight: size === 'lg' ? 600 : 500,
          fontSize: size === 'lg' ? 15 : 13,
          color: size === 'lg' ? meta.color : 'inherit',
        }}
      >
        {label ?? meta.label}
      </span>
    </span>
  )
}

// The per-bucket route counts (operational / degraded / down / disabled). Zero
// buckets are hidden to keep the row scannable — except operational, which
// always shows so an all-healthy env still reads as "5 operational".
export function HealthChips({ summary }: { summary: HealthSummary }) {
  const chips: { n: number; label: string; color: string; tip: string; force?: boolean }[] = [
    { n: summary.operational, label: 'operational', color: color.verified, tip: 'Enabled routes with every backend healthy', force: true },
    { n: summary.degraded, label: 'degraded', color: color.signal, tip: 'Enabled routes with some backend unhealthy or health not yet known' },
    { n: summary.down, label: 'down', color: color.fail, tip: 'Enabled routes with no healthy backend' },
    { n: summary.disabled, label: 'disabled', color: '#9b9eaa', tip: 'Routes turned off — not serving traffic' },
  ]
  return (
    <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', alignItems: 'center' }}>
      {chips
        .filter((c) => c.n > 0 || c.force)
        .map((c) => (
          <Tooltip key={c.label} title={c.tip}>
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 13, cursor: 'default' }}>
              <span style={{ width: 8, height: 8, borderRadius: 999, background: c.color, flex: 'none' }} />
              <span style={{ fontVariantNumeric: 'tabular-nums', fontWeight: 600 }}>{c.n}</span>
              <span style={{ color: color.ink500 }}>{c.label}</span>
            </span>
          </Tooltip>
        ))}
    </div>
  )
}

export function GatewayStatusBar({
  summary,
  gatewayEnabled,
  baseUrl,
  envCount,
}: {
  summary: HealthSummary
  gatewayEnabled?: boolean
  baseUrl?: string
  envCount: number
}) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 20,
        flexWrap: 'wrap',
        padding: '14px 18px',
        background: color.paperRaised,
        border: `1px solid ${color.paperBorder}`,
        borderRadius: 10,
        marginBottom: 20,
      }}
    >
      <StatusDot status={summary.status} size="lg" />
      <span style={{ width: 1, height: 22, background: color.paperBorder }} />
      <HealthChips summary={summary} />
      <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 16, fontSize: 13, color: color.ink500 }}>
        {gatewayEnabled === false ? (
          <span style={{ color: color.fail }}>Gateway integration disabled</span>
        ) : baseUrl ? (
          <Tooltip title="Control-plane gateway base URL">
            <span className="agenda-mono" style={{ fontSize: 12 }}>
              {baseUrl}
            </span>
          </Tooltip>
        ) : null}
        <span>
          <b style={{ color: color.ink900, fontVariantNumeric: 'tabular-nums' }}>{summary.routes}</b> routes
          {envCount > 0 ? (
            <>
              {' · '}
              <b style={{ color: color.ink900, fontVariantNumeric: 'tabular-nums' }}>{envCount}</b> env
              {envCount > 1 ? 's' : ''}
            </>
          ) : null}
        </span>
      </div>
    </div>
  )
}
