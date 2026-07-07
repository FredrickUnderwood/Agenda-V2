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

// panelId 1 = error rate (5xx), 2 = P99 latency — see
// deploy/observability/grafana/dashboards/gateway-overview.json
export function panelEmbedUrl(baseUrl: string, panelId: number): string {
  return `${baseUrl.replace(/\/+$/, '')}/d-solo/agenda-gateway-overview/gateway-overview?panelId=${panelId}&theme=light&kiosk`
}
