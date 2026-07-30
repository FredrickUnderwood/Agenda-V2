import type {
  Application,
  ApplicationEnvTarget,
  ApplicationGatewayRoute,
  Environment,
  GatewayBackendMode,
} from '@/api/types'

// The gateway visualization aggregates the per-application, per-env route config
// (routes are echoed onto every target of an env — see targetPayload.ts) into a
// single request-flow model: inbound Host → Route → Service (an app+env) and the
// backend Instances each route actually reaches. There's no global routes API, so
// this is assembled client-side from listApplications + per-app instances.

export interface GwInstance {
  targetId: number
  name: string
  enabled: boolean
  health: string
  machineId: number
  port: number
  weight?: number // only set when a 'selected' route weights this backend
}

export interface GwRoute {
  id: string // `${appId}:${env}:${routeKey}`
  appId: number
  appName: string
  env: Environment
  routeKey: string
  host: string // '' = any host
  pathPrefix: string
  enabled: boolean
  backendMode: GatewayBackendMode
  hostId: string // host || '*'
  serviceId: string // `${appId}:${env}`
  instances: GwInstance[] // resolved backends this route hits
}

export interface GwService {
  id: string // `${appId}:${env}`
  appId: number
  appName: string
  env: Environment
  instances: GwInstance[]
}

export interface GwHost {
  id: string // host || '*'
  host: string // '' = any host
}

export interface GatewayModel {
  hosts: GwHost[]
  routes: GwRoute[]
  services: GwService[]
}

export interface AppInstances {
  app: Application
  targets: ApplicationEnvTarget[]
}

function toInstance(t: ApplicationEnvTarget): GwInstance {
  return {
    targetId: t.id,
    name: t.display_name || t.instance_name,
    enabled: t.enabled,
    health: t.health?.status ?? 'unknown',
    machineId: t.machine_id,
    port: t.port,
  }
}

// Which backend instances a route resolves to. 'selected' pins specific targets
// (with weights); 'all_enabled'/'single' fan out to the env's enabled instances.
function resolveRouteInstances(r: ApplicationGatewayRoute, envInstances: GwInstance[]): GwInstance[] {
  if (r.backend_mode === 'selected') {
    const byId = new Map(envInstances.map((i) => [i.targetId, i]))
    const out: GwInstance[] = []
    for (const b of r.backends ?? []) {
      const inst = byId.get(b.target_id)
      if (inst) out.push({ ...inst, weight: b.weight })
    }
    return out
  }
  return envInstances.filter((i) => i.enabled)
}

// '*' (any host) sorts last; envs sort in a stable, meaningful order.
const ENV_ORDER: Record<string, number> = { prod: 0, stage: 1, test: 2 }

function hostSort(a: string, b: string): number {
  if (a === '*' && b !== '*') return 1
  if (b === '*' && a !== '*') return -1
  return a.localeCompare(b)
}

export function buildGatewayModel(entries: AppInstances[]): GatewayModel {
  const hosts = new Map<string, GwHost>()
  const services = new Map<string, GwService>()
  const routes: GwRoute[] = []

  for (const { app, targets } of entries) {
    const byEnv = new Map<Environment, ApplicationEnvTarget[]>()
    for (const t of targets) {
      const arr = byEnv.get(t.env) ?? []
      arr.push(t)
      byEnv.set(t.env, arr)
    }

    for (const [env, envTargets] of byEnv) {
      const serviceId = `${app.id}:${env}`
      const instances = envTargets.map(toInstance)
      services.set(serviceId, { id: serviceId, appId: app.id, appName: app.name, env, instances })

      // Routes are echoed onto every target of the env — the first is authoritative.
      const rawRoutes = envTargets[0]?.gateway_routes ?? []
      for (const r of rawRoutes) {
        const hostId = r.host || '*'
        if (!hosts.has(hostId)) hosts.set(hostId, { id: hostId, host: r.host })
        routes.push({
          id: `${app.id}:${env}:${r.route_key}`,
          appId: app.id,
          appName: app.name,
          env,
          routeKey: r.route_key,
          host: r.host,
          pathPrefix: r.path_prefix,
          enabled: r.enabled,
          backendMode: r.backend_mode,
          hostId,
          serviceId,
          instances: resolveRouteInstances(r, instances),
        })
      }
    }
  }

  const hostList = [...hosts.values()].sort((a, b) => hostSort(a.id, b.id))
  const hostRank = new Map(hostList.map((h, i) => [h.id, i]))

  // Order routes so a host's routes stay contiguous (fewer edge crossings in the
  // topology), then by service, then path.
  routes.sort(
    (a, b) =>
      (hostRank.get(a.hostId)! - hostRank.get(b.hostId)!) ||
      a.appName.localeCompare(b.appName) ||
      (ENV_ORDER[a.env] ?? 9) - (ENV_ORDER[b.env] ?? 9) ||
      a.pathPrefix.localeCompare(b.pathPrefix),
  )

  const serviceList = [...services.values()].sort(
    (a, b) => a.appName.localeCompare(b.appName) || (ENV_ORDER[a.env] ?? 9) - (ENV_ORDER[b.env] ?? 9),
  )

  return { hosts: hostList, routes, services: serviceList }
}

export function healthTone(status: string): 'verified' | 'failed' | 'building' | 'idle' {
  switch (status) {
    case 'healthy':
    case 'verified':
    case 'ok':
      return 'verified'
    case 'unhealthy':
    case 'failed':
      return 'failed'
    case 'unknown':
    case '':
      return 'idle'
    default:
      return 'building'
  }
}
