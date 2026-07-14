// The control plane has no API for "where's Grafana" (deploy/observability is
// an optional, independently-deployed add-on — see its README), so this is a
// client-side setting: default from an env var, overridable per-browser.
const STORAGE_KEY = 'agenda.grafanaBaseUrl'
const DEFAULT_URL = import.meta.env.VITE_GRAFANA_BASE_URL ?? 'http://localhost:3000'

export function getGrafanaBaseUrl(): string {
  return localStorage.getItem(STORAGE_KEY) ?? DEFAULT_URL
}

export function setGrafanaBaseUrl(url: string): void {
  localStorage.setItem(STORAGE_KEY, url.replace(/\/+$/, ''))
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

// panelId 1 = error rate (5xx), 2 = P99 latency — see
// deploy/observability/grafana/dashboards/gateway-overview.json
//
// kiosk mode hides Grafana's own time-range picker on the embedded panel, so
// without an explicit from/to every request falls back to whatever range is
// saved in the dashboard JSON — the picker in MonitoringTab is the only way
// to change it. refresh mirrors the picker choice so bumping the range also
// bumps how often the iframe re-queries it.
export function panelEmbedUrl(baseUrl: string, panelId: number, from: string): string {
  const params = new URLSearchParams({
    panelId: String(panelId),
    theme: 'light',
    kiosk: '',
    from,
    to: 'now',
    refresh: '30s',
  })
  return `${baseUrl.replace(/\/+$/, '')}/d-solo/agenda-gateway-overview/gateway-overview?${params.toString()}`
}
