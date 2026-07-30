import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Empty, Tag } from 'antd'
import { color } from '@/theme/tokens'
import type { GatewayModel, GwInstance } from './model'
import { healthTone } from './model'

// A layered request-flow graph: Inbound host → Route → Service. Nodes are HTML
// (so they can use antd Tags / the mono font), edges are an SVG overlay behind
// them. Columns are fixed-x; nodes stack vertically and each column is centered,
// so the layout is deterministic without a graph-layout dependency.

const COL_X = [0, 300, 640]
const NODE_W = [200, 220, 300]
const VGAP = 18
const HEADER = 30
const HOST_H = 44
const ROUTE_H = 64
const SVC_HEADER = 44
const SVC_ROW = 22
const SVC_PAD = 14

const DOT_COLOR: Record<ReturnType<typeof healthTone>, string> = {
  verified: color.verified,
  failed: color.fail,
  building: color.signal,
  idle: '#9b9eaa',
}

interface Box {
  x: number
  y: number
  w: number
  h: number
}

interface Edge {
  from: string
  to: string
  disabled: boolean
}

const COLUMN_TITLES = ['Inbound host', 'Route', 'Service']

export function GatewayTopology({ model }: { model: GatewayModel }) {
  const navigate = useNavigate()
  const [hovered, setHovered] = useState<string | null>(null)

  const layout = useMemo(() => computeLayout(model), [model])
  // Nodes/edges to emphasize when hovering. Empty set = nothing hovered (all normal).
  const active = useMemo(() => activeSet(model, hovered), [model, hovered])

  if (model.routes.length === 0) {
    return <Empty description="No gateway routes configured yet" style={{ padding: '60px 0' }} />
  }

  const { pos, width, height, edges, shownServices } = layout
  const dim = (id: string) => hovered !== null && !active.has(id)

  return (
    <div style={{ overflowX: 'auto', paddingBottom: 8 }}>
      <div style={{ position: 'relative', width, height, minWidth: width }}>
        {/* Column headers */}
        {COLUMN_TITLES.map((t, i) => (
          <div
            key={t}
            style={{
              position: 'absolute',
              left: COL_X[i],
              top: 0,
              width: NODE_W[i],
              fontSize: 11,
              fontWeight: 600,
              letterSpacing: '0.04em',
              textTransform: 'uppercase',
              color: color.ink500,
            }}
          >
            {t}
          </div>
        ))}

        {/* Edges */}
        <svg width={width} height={height} style={{ position: 'absolute', inset: 0, pointerEvents: 'none' }}>
          {edges.map((e, i) => {
            const a = pos.get(e.from)
            const b = pos.get(e.to)
            if (!a || !b) return null
            const x1 = a.x + a.w
            const y1 = a.y + a.h / 2
            const x2 = b.x
            const y2 = b.y + b.h / 2
            const dx = Math.max((x2 - x1) * 0.4, 30)
            const isActive = hovered !== null && active.has(e.from) && active.has(e.to)
            const isDim = hovered !== null && !isActive
            return (
              <path
                key={i}
                d={`M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`}
                fill="none"
                stroke={isActive ? color.signal : '#CFCABC'}
                strokeWidth={isActive ? 2.5 : 1.5}
                strokeDasharray={e.disabled ? '4 4' : undefined}
                opacity={isDim ? 0.12 : e.disabled ? 0.55 : 0.9}
              />
            )
          })}
        </svg>

        {/* Host nodes */}
        {model.hosts.map((h) => {
          const box = pos.get(h.id)
          if (!box) return null
          return (
            <NodeShell key={h.id} box={box} dim={dim(h.id)} onHover={() => setHovered(h.id)} onLeave={() => setHovered(null)}>
              <div className="agenda-mono" style={{ fontSize: 13, fontWeight: 500, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {h.host || '★ any host'}
              </div>
            </NodeShell>
          )
        })}

        {/* Route nodes */}
        {model.routes.map((r) => {
          const box = pos.get(r.id)
          if (!box) return null
          return (
            <NodeShell
              key={r.id}
              box={box}
              dim={dim(r.id)}
              disabled={!r.enabled}
              onClick={() => navigate(`/applications/${r.appId}`)}
              onHover={() => setHovered(r.id)}
              onLeave={() => setHovered(null)}
            >
              <div className="agenda-mono" style={{ fontSize: 13, fontWeight: 600, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {r.pathPrefix || '/'}
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginTop: 4 }}>
                <span style={{ fontSize: 12, color: color.ink500 }}>{r.routeKey}</span>
                <Tag color={r.env === 'prod' ? 'orange' : undefined} style={{ marginInlineEnd: 0, transform: 'scale(0.85)', transformOrigin: 'left' }}>
                  {r.env}
                </Tag>
                {!r.enabled ? (
                  <Tag style={{ marginInlineEnd: 0, transform: 'scale(0.85)', transformOrigin: 'left' }}>disabled</Tag>
                ) : null}
              </div>
            </NodeShell>
          )
        })}

        {/* Service nodes */}
        {shownServices.map((s) => {
          const box = pos.get(s.id)
          if (!box) return null
          return (
            <NodeShell key={s.id} box={box} dim={dim(s.id)} onClick={() => navigate(`/applications/${s.appId}`)} onHover={() => setHovered(s.id)} onLeave={() => setHovered(null)}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span className="agenda-display" style={{ fontSize: 14, fontWeight: 600 }}>
                  {s.appName}
                </span>
                <Tag color={s.env === 'prod' ? 'orange' : undefined} style={{ marginInlineEnd: 0 }}>
                  {s.env}
                </Tag>
              </div>
              <div style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 4 }}>
                {s.instances.map((i) => (
                  <InstanceRow key={i.targetId} inst={i} />
                ))}
              </div>
            </NodeShell>
          )
        })}
      </div>

      <Legend />
    </div>
  )
}

function NodeShell({
  box,
  dim,
  disabled,
  children,
  onClick,
  onHover,
  onLeave,
}: {
  box: Box
  dim: boolean
  disabled?: boolean
  children: React.ReactNode
  onClick?: () => void
  onHover: () => void
  onLeave: () => void
}) {
  return (
    <div
      onMouseEnter={onHover}
      onMouseLeave={onLeave}
      onClick={onClick}
      style={{
        position: 'absolute',
        left: box.x,
        top: box.y,
        width: box.w,
        height: box.h,
        boxSizing: 'border-box',
        padding: '8px 12px',
        background: color.paperRaised,
        border: `1px ${disabled ? 'dashed' : 'solid'} ${color.paperBorder}`,
        borderRadius: 8,
        boxShadow: '0 1px 2px rgba(20,21,26,0.04)',
        cursor: onClick ? 'pointer' : 'default',
        opacity: dim ? 0.35 : 1,
        transition: 'opacity 0.12s ease, border-color 0.12s ease, box-shadow 0.12s ease',
        overflow: 'hidden',
      }}
    >
      {children}
    </div>
  )
}

function InstanceRow({ inst }: { inst: GwInstance }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, opacity: inst.enabled ? 1 : 0.5 }}>
      <span style={{ width: 7, height: 7, borderRadius: 999, flex: 'none', background: DOT_COLOR[healthTone(inst.health)] }} />
      <span className="agenda-mono" style={{ whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
        {inst.name}
      </span>
      {inst.weight != null ? <span style={{ color: color.ink500, fontSize: 11 }}>w{inst.weight}</span> : null}
      {!inst.enabled ? <span style={{ color: color.ink500, fontSize: 11 }}>off</span> : null}
    </div>
  )
}

function Legend() {
  const item = (c: string, label: string) => (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
      <span style={{ width: 8, height: 8, borderRadius: 999, background: c }} />
      <span style={{ fontSize: 12, color: color.ink500 }}>{label}</span>
    </span>
  )
  return (
    <div style={{ display: 'flex', gap: 18, flexWrap: 'wrap', marginTop: 16, alignItems: 'center' }}>
      {item(color.verified, 'healthy')}
      {item(color.fail, 'unhealthy')}
      {item('#9b9eaa', 'unknown')}
      <span style={{ fontSize: 12, color: color.ink500 }}>Dashed edge / border = route disabled · hover to trace a path · click a route or service to open it</span>
    </div>
  )
}

// ── layout ────────────────────────────────────────────────────────────────

function serviceHeight(n: number): number {
  return SVC_HEADER + Math.max(n, 1) * SVC_ROW + SVC_PAD
}

function computeLayout(model: GatewayModel) {
  const routeServiceIds = new Set(model.routes.map((r) => r.serviceId))
  const shownServices = model.services.filter((s) => routeServiceIds.has(s.id))

  const cols: { id: string; h: number }[][] = [
    model.hosts.map((h) => ({ id: h.id, h: HOST_H })),
    model.routes.map((r) => ({ id: r.id, h: ROUTE_H })),
    shownServices.map((s) => ({ id: s.id, h: serviceHeight(s.instances.length) })),
  ]

  const colHeights = cols.map((items) => items.reduce((sum, it) => sum + it.h, 0) + VGAP * Math.max(items.length - 1, 0))
  const bodyH = Math.max(...colHeights, 0)
  const height = HEADER + bodyH
  const width = COL_X[COL_X.length - 1] + NODE_W[NODE_W.length - 1]

  const pos = new Map<string, Box>()
  cols.forEach((items, ci) => {
    let y = HEADER + (bodyH - colHeights[ci]) / 2
    for (const it of items) {
      pos.set(it.id, { x: COL_X[ci], y, w: NODE_W[ci], h: it.h })
      y += it.h + VGAP
    }
  })

  const edges: Edge[] = []
  for (const r of model.routes) {
    edges.push({ from: r.hostId, to: r.id, disabled: !r.enabled })
    edges.push({ from: r.id, to: r.serviceId, disabled: !r.enabled })
  }

  return { pos, width, height, edges, shownServices }
}

// Node ids to highlight for the hovered node (the node + its direct neighbours,
// one hop each way through the Host→Route→Service chain).
function activeSet(model: GatewayModel, hovered: string | null): Set<string> {
  const set = new Set<string>()
  if (hovered === null) return set
  set.add(hovered)
  for (const r of model.routes) {
    if (r.hostId === hovered || r.id === hovered || r.serviceId === hovered) {
      set.add(r.hostId)
      set.add(r.id)
      set.add(r.serviceId)
    }
  }
  return set
}
