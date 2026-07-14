// deploy_config is stored on the backend as a JSON string (Application.deploy_config),
// but the UI edits it as structured form fields to keep the learning cost low.
// These helpers translate between the stored JSON and the form value shapes.
// Mirrors internal/domain/application.go (DockerDeployConfig / APIDeployConfig).

import type { DeployMethod } from '@/api/types'

export interface KV {
  key?: string
  value?: string
}

export interface DockerHealthCheckForm {
  enabled: boolean
  require_healthy?: boolean
  timeout_seconds?: number
  interval_seconds?: number
}

export interface DockerConfigForm {
  machine?: string
  work_dir?: string
  compose_file?: string
  services?: string[]
  pull_before_deploy?: boolean
  pre_commands?: string[]
  env?: KV[]
  health_check: DockerHealthCheckForm
}

export interface ApiConfigForm {
  url?: string
  method?: string
  headers?: KV[]
  body?: string
  expected_status?: number
  timeout_seconds?: number
}

export interface DeployConfigForm {
  docker: DockerConfigForm
  api: ApiConfigForm
}

function safeParse(json: string): Record<string, unknown> {
  if (!json || !json.trim()) return {}
  try {
    const v = JSON.parse(json)
    return v && typeof v === 'object' ? (v as Record<string, unknown>) : {}
  } catch {
    return {}
  }
}

function mapToKV(m: unknown): KV[] {
  if (!m || typeof m !== 'object') return []
  return Object.entries(m as Record<string, unknown>).map(([key, value]) => ({
    key,
    value: value == null ? '' : String(value),
  }))
}

function kvToMap(kvs: KV[] | undefined): Record<string, string> {
  const out: Record<string, string> = {}
  for (const { key, value } of kvs ?? []) {
    const k = (key ?? '').trim()
    if (k) out[k] = value ?? ''
  }
  return out
}

function strList(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((s): s is string => typeof s === 'string') : []
}

// parse turns the stored JSON into both branch shapes so switching deploy
// method in the UI keeps whatever the user already entered for each branch.
export function parseDeployConfig(json: string, method: DeployMethod): DeployConfigForm {
  const c = safeParse(json)
  const hc = (c.health_check ?? {}) as Record<string, unknown>
  return {
    docker: {
      machine: typeof c.machine === 'string' ? c.machine : undefined,
      work_dir: typeof c.work_dir === 'string' ? c.work_dir : undefined,
      compose_file: typeof c.compose_file === 'string' ? c.compose_file : undefined,
      services: strList(c.services),
      pull_before_deploy: c.pull_before_deploy === true,
      pre_commands: strList(c.pre_commands),
      env: method === 'docker' ? mapToKV(c.env) : [],
      health_check: {
        // Backend treats absent/null enabled as true (see composeHealthCheckEnabled).
        enabled: hc.enabled !== false,
        require_healthy: hc.require_healthy === true,
        timeout_seconds: typeof hc.timeout_seconds === 'number' ? hc.timeout_seconds : undefined,
        interval_seconds: typeof hc.interval_seconds === 'number' ? hc.interval_seconds : undefined,
      },
    },
    api: {
      url: typeof c.url === 'string' ? c.url : undefined,
      method: typeof c.method === 'string' ? c.method : undefined,
      headers: method === 'api' ? mapToKV(c.headers) : [],
      body: typeof c.body === 'string' ? c.body : undefined,
      expected_status: typeof c.expected_status === 'number' ? c.expected_status : undefined,
      timeout_seconds: typeof c.timeout_seconds === 'number' ? c.timeout_seconds : undefined,
    },
  }
}

// build serializes the active branch of the form back into the stored JSON
// string. Only the fields for the selected deploy method are emitted.
export function buildDeployConfig(form: DeployConfigForm, method: DeployMethod): string {
  if (method === 'api') {
    const a = form.api
    const cfg: Record<string, unknown> = {
      url: a.url ?? '',
      method: a.method ?? '',
    }
    const headers = kvToMap(a.headers)
    if (Object.keys(headers).length) cfg.headers = headers
    if (a.body) cfg.body = a.body
    if (a.expected_status) cfg.expected_status = a.expected_status
    if (a.timeout_seconds) cfg.timeout_seconds = a.timeout_seconds
    return JSON.stringify(cfg)
  }

  const d = form.docker
  const cfg: Record<string, unknown> = { work_dir: d.work_dir ?? '' }
  if (d.machine) cfg.machine = d.machine
  if (d.compose_file) cfg.compose_file = d.compose_file
  const services = (d.services ?? []).map((s) => s.trim()).filter(Boolean)
  if (services.length) cfg.services = services
  cfg.pull_before_deploy = !!d.pull_before_deploy
  const preCommands = (d.pre_commands ?? []).map((s) => s ?? '').filter((s) => s.trim())
  if (preCommands.length) cfg.pre_commands = preCommands
  const env = kvToMap(d.env)
  if (Object.keys(env).length) cfg.env = env

  const hc = d.health_check
  const health: Record<string, unknown> = { enabled: hc.enabled }
  if (hc.require_healthy) health.require_healthy = true
  if (hc.timeout_seconds) health.timeout_seconds = hc.timeout_seconds
  if (hc.interval_seconds) health.interval_seconds = hc.interval_seconds
  cfg.health_check = health

  return JSON.stringify(cfg)
}
