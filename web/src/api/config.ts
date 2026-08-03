import { apiClient } from './client'

// Subset of GET /config the gateway view needs — whether the control plane has a
// gateway wired up, and its base URL. Secrets in the full payload are redacted
// server-side (internal/handler/config_handler.go).
export interface AppConfig {
  gateway: {
    enabled: boolean
    base_url: string
  }
}

export function getConfig() {
  return apiClient.get<AppConfig>('/config').then((r) => r.data)
}
