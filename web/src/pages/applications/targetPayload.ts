import type {
  ApplicationEnvTarget,
  ApplicationEnvTargetRequest,
  ApplicationGatewayRoute,
  ApplicationGatewayRouteRequest,
  Environment,
} from '@/api/types'

// The control plane models gateway routes as env-scoped: every target of one
// environment echoes the SAME route list in responses, and on write the routes
// accumulated across an env's target requests fully REPLACE that env's routes
// (application_service.syncTargets → SyncByApplicationEnv). Two consequences a
// naive `updateApplication({ targets })` round-trip gets wrong:
//
//   1. Dropping gateway_routes from a target request deletes every route in
//      that env — so any instance edit would silently wipe route config.
//   2. Attaching the (identical) route list to more than one target of the
//      same env double-counts it and trips the duplicate-route validation.
//
// buildTargetsPayload rebuilds a complete, safe targets payload: it carries
// each env's routes on exactly one representative target and sends [] on the
// rest, so unrelated edits preserve routes and multi-instance envs don't
// duplicate them.

export function targetToRequest(t: ApplicationEnvTarget): ApplicationEnvTargetRequest {
  return {
    env: t.env,
    instance_name: t.instance_name,
    display_name: t.display_name,
    machine_id: t.machine_id,
    port: t.port,
    enabled: t.enabled,
    health_check_enabled: t.health_check_enabled,
    health_check_path: t.health_check_path,
    metrics_enabled: t.metrics_enabled,
    metrics_port: t.metrics_port,
  }
}

export function routeToRequest(r: ApplicationGatewayRoute): ApplicationGatewayRouteRequest {
  return {
    id: r.id,
    route_key: r.route_key,
    host: r.host,
    path_prefix: r.path_prefix,
    strip_prefix: r.strip_prefix,
    backend_path: r.backend_path,
    enabled: r.enabled,
    backend_mode: r.backend_mode,
    instance_select_mode: r.instance_select_mode,
    instance_header: r.instance_header,
    sort_order: r.sort_order,
    backends: (r.backends ?? []).map((b) => ({
      target_id: b.target_id,
      weight: b.weight,
      enabled: b.enabled,
    })),
  }
}

// buildTargetsPayload converts the fetched targets into a write payload,
// preserving each env's existing routes on one representative target. Pass
// routeOverride to replace a single env's route list (the RoutesTab uses this
// when adding/editing/removing a route).
export function buildTargetsPayload(
  targets: ApplicationEnvTarget[],
  extraTargets: ApplicationEnvTargetRequest[] = [],
  routeOverride?: { env: Environment; routes: ApplicationGatewayRouteRequest[] },
): ApplicationEnvTargetRequest[] {
  const seenEnv = new Set<Environment>()
  const out: ApplicationEnvTargetRequest[] = []

  for (const t of targets) {
    const req = targetToRequest(t)
    if (seenEnv.has(t.env)) {
      req.gateway_routes = []
    } else {
      seenEnv.add(t.env)
      req.gateway_routes =
        routeOverride && routeOverride.env === t.env
          ? routeOverride.routes
          : (t.gateway_routes ?? []).map(routeToRequest)
    }
    out.push(req)
  }

  // Newly-added instances carry no routes of their own; if they land in an env
  // that already has a representative, they contribute [] like any sibling.
  for (const req of extraTargets) {
    if (seenEnv.has(req.env)) {
      out.push({ ...req, gateway_routes: [] })
    } else {
      seenEnv.add(req.env)
      out.push({
        ...req,
        gateway_routes:
          routeOverride && routeOverride.env === req.env ? routeOverride.routes : (req.gateway_routes ?? []),
      })
    }
  }

  return out
}

// routesForEnv returns the (deduped) route list for one env — routes are
// echoed onto every target, so the first target of the env is authoritative.
export function routesForEnv(targets: ApplicationEnvTarget[], env: Environment): ApplicationGatewayRoute[] {
  const t = targets.find((x) => x.env === env)
  return t?.gateway_routes ?? []
}
