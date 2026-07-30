import type { ReactNode } from 'react'
import type { SettingType } from '@/api/types'

// The Settings page presents a curated, "traditional software settings" form
// over what is a generic key/value store on the backend (internal/domain.Setting).
// This registry is the single source of truth for the well-known keys the control
// plane and gateway actually read — grouped into sections, each key rendered as a
// labeled field instead of making the operator remember the key string and type.
//
// Keep this in sync with the Go consumers:
//   - gateway.tls.*      internal/service/gateway_tls_sync_service.go
//   - observability.*    internal/handler/middleware.go + internal/application/alert_rule_monitor.go
// The git.token.* and alert.channel.* namespaces are variable-cardinality
// (per-host / per-channel) so they get their own dedicated collection editors
// rather than a fixed field list here.

export type SettingFieldInput = 'text' | 'password' | 'textarea'

export interface SettingFieldSpec {
  key: string
  label: string
  input: SettingFieldInput
  // Backend Setting.type (mostly 'string'); drives how the value is stored.
  type: SettingType
  // Whether the value is encrypted at rest and redacted in the list API. Secret
  // fields can never be prefilled — the form only ever sends a newly typed value.
  secret: boolean
  required?: boolean
  placeholder?: string
  help?: ReactNode
}

export interface SettingSectionSpec {
  id: string
  title: string
  description: ReactNode
  fields: SettingFieldSpec[]
}

export const GATEWAY_TLS_SECTION: SettingSectionSpec = {
  id: 'gateway-tls',
  title: 'Gateway edge TLS',
  description:
    'When the gateway runs as the TLS edge (GATEWAY_TLS_ENABLED), it auto-issues HTTPS certificates for every route host via ACME DNS-01. The control plane pushes these credentials to the gateway; secrets are encrypted at rest and never stored on the gateway. Prerequisites: each domain’s DNS must be hosted on Aliyun (NS → *.alidns.com), and the RAM key needs AliyunDNSFullAccess. The first issue of a new domain can take a few minutes to propagate.',
  fields: [
    {
      key: 'gateway.tls.acme_email',
      label: 'ACME account email',
      input: 'text',
      type: 'string',
      secret: false,
      required: true,
      placeholder: 'ops@example.com',
    },
    {
      key: 'gateway.tls.aliyun_ak_id',
      label: 'Aliyun AccessKey ID',
      input: 'password',
      type: 'string',
      secret: true,
      required: true,
      help: 'Aliyun RAM AccessKey ID — needs AliyunDNSFullAccess.',
    },
    {
      key: 'gateway.tls.aliyun_ak_secret',
      label: 'Aliyun AccessKey Secret',
      input: 'password',
      type: 'string',
      secret: true,
      required: true,
      help: 'Aliyun RAM AccessKey Secret.',
    },
    {
      key: 'gateway.tls.acme_ca',
      label: 'ACME directory URL',
      input: 'text',
      type: 'string',
      secret: false,
      placeholder: 'https://acme.zerossl.com/v2/DV90',
      help: 'Optional — defaults to ZeroSSL DV90.',
    },
    {
      key: 'gateway.tls.dns_provider',
      label: 'DNS-01 provider',
      input: 'text',
      type: 'string',
      secret: false,
      placeholder: 'alidns',
      help: 'Optional — defaults to alidns.',
    },
    {
      key: 'gateway.tls.eab_kid',
      label: 'ZeroSSL EAB key id',
      input: 'password',
      type: 'string',
      secret: true,
      help: 'Required only when the ACME CA is ZeroSSL.',
    },
    {
      key: 'gateway.tls.eab_hmac',
      label: 'ZeroSSL EAB HMAC key',
      input: 'password',
      type: 'string',
      secret: true,
      help: 'Required only when the ACME CA is ZeroSSL.',
    },
    {
      key: 'gateway.tls.static_domains',
      label: 'Extra static domains',
      input: 'textarea',
      type: 'string',
      secret: false,
      placeholder: 'example.com api.example.com',
      help: 'Extra domains to certify beyond route hosts (space- or comma-separated).',
    },
  ],
}

export const OBSERVABILITY_SECTION: SettingSectionSpec = {
  id: 'observability',
  title: 'Observability',
  description:
    'How the control plane reaches Prometheus for alert-rule evaluation, and the bearer token that gates metrics scraping of the control plane itself.',
  fields: [
    {
      key: 'observability.prometheus_url',
      label: 'Prometheus base URL',
      input: 'text',
      type: 'string',
      secret: false,
      placeholder: 'http://prometheus:9090',
      help: 'Base URL the alert-rule engine queries with instant PromQL.',
    },
    {
      key: 'observability.scrape_token',
      label: 'Metrics scrape token',
      input: 'password',
      type: 'string',
      secret: true,
      help: 'Bearer token required on the control plane’s /metrics endpoint. Leave unset to keep /metrics open.',
    },
  ],
}

// Sections rendered by the generic FixedSettingsForm. Order = tab order.
export const FIXED_SECTIONS: SettingSectionSpec[] = [GATEWAY_TLS_SECTION, OBSERVABILITY_SECTION]

// Every key owned by a purpose-built editor (fixed sections + the dynamic
// collections). The Advanced tab surfaces anything NOT in this set / prefix list
// so no setting is ever invisible in the UI.
export const KNOWN_KEYS = new Set<string>(FIXED_SECTIONS.flatMap((s) => s.fields.map((f) => f.key)))

export const KNOWN_PREFIXES = ['git.token.', 'alert.channel.']

export function isManagedKey(key: string): boolean {
  return KNOWN_KEYS.has(key) || KNOWN_PREFIXES.some((p) => key.startsWith(p))
}
