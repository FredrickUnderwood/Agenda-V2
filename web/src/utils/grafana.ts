// Grafana is reverse-proxied at same-origin /grafana by the web console's nginx
// (see web/nginx.conf) — Grafana's own port is never exposed. Panel URLs are
// therefore same-origin relative paths; no per-browser base URL to configure.
// Override VITE_GRAFANA_BASE_URL at build time only if Grafana lives elsewhere.
const DEFAULT_BASE = import.meta.env.VITE_GRAFANA_BASE_URL ?? '/grafana'

export function getGrafanaBaseUrl(): string {
  return DEFAULT_BASE
}

// Preset time ranges for the panel picker — Grafana's own relative-time
// syntax (https://grafana.com/docs/grafana/latest/dashboards/use-dashboards/#time-units-and-relative-ranges),
// passed straight through as the d-solo iframe's from/to.
export const TIME_RANGE_OPTIONS = [
  { label: 'Last 15m', from: 'now-15m' },
  { label: 'Last 1h', from: 'now-1h' },
  { label: 'Last 6h', from: 'now-6h' },
  { label: 'Last 24h', from: 'now-24h' },
] as const

export const DEFAULT_TIME_RANGE_FROM: string = TIME_RANGE_OPTIONS[1].from

// Env filter for the panels, passed as the dashboard's `env` template variable
// (var-env). The values are the domain.Environment strings the gateway and the
// app-metrics scrape job both label their series with, and '.*' matches them
// all — the dashboard interpolates the variable into an `env=~"$env"` matcher.
//
// Without this every panel summed prod, stage and test together (they were
// aggregated away by route/endpoint), so a load test on test env moved what
// looked like a production error-rate line.
export const ENV_FILTER_OPTIONS = [
  { label: 'Production', value: 'prod' },
  { label: 'Staging', value: 'stage' },
  { label: 'Test', value: 'test' },
  { label: 'All envs', value: '.*' },
] as const

// Production, not 'All envs': the mixed view is what made these panels
// misleading, so the default has to name one env. The picker sits next to the
// time range, so another env is one click away.
export const DEFAULT_ENV_FILTER: string = ENV_FILTER_OPTIONS[0].value

// panelId 1 = error rate (5xx), 2 = P99 latency — see
// deploy/observability/grafana/dashboards/gateway-overview.json
//
// serviceName is passed as the dashboard's `service` template variable
// (var-service) so the panel shows only the current app's series — the
// dashboard expr filters on service_name=~"$service". env narrows that to one
// environment the same way (var-env → env=~"$env").
//
// kiosk mode hides Grafana's own time-range picker on the embedded panel, so
// without an explicit from/to every request falls back to whatever range is
// saved in the dashboard JSON — the picker in MonitoringTab is the only way
// to change it. refresh mirrors the picker choice so bumping the range also
// bumps how often the iframe re-queries it.
export function panelEmbedUrl(
  baseUrl: string,
  panelId: number,
  from: string,
  serviceName: string,
  env: string,
): string {
  const params = new URLSearchParams({
    panelId: String(panelId),
    theme: 'light',
    kiosk: '',
    from,
    to: 'now',
    refresh: '30s',
    'var-service': serviceName,
    'var-env': env,
  })
  return `${baseUrl.replace(/\/+$/, '')}/d-solo/agenda-gateway-overview/gateway-overview?${params.toString()}`
}
