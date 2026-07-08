// Mirrors internal/domain + internal/service request/response shapes on the
// control plane. Kept as one file since the backend has no OpenAPI spec to
// generate from — update alongside Go changes.

export type Environment = 'prod' | 'stage' | 'test'
export type DeployMethod = 'docker' | 'api'
export type AuthType = 'sshkey' | 'password'
export type MachineMode = 'ssh' | 'agent'
export type ReleaseStatus =
  | 'draft'
  | 'deploying'
  | 'pending_verify'
  | 'verified'
  | 'failed'
  | 'rolling_back'
  | 'rolled_back'
export type SettingType = 'string' | 'int' | 'bool' | 'json'
export type GatewayBackendMode = 'single' | 'all_enabled' | 'selected'
export type GatewayInstanceSelectMode = 'disabled' | 'enabled'

export interface Application {
  id: number
  name: string
  repo_url: string
  deploy_method: DeployMethod
  deploy_config: string
  description: string
  created_at: string
  updated_at: string
  targets?: ApplicationEnvTarget[]
}

export interface ApplicationGatewayRouteBackend {
  id: number
  route_id: number
  target_id: number
  weight: number
  enabled: boolean
}

export interface ApplicationGatewayRoute {
  id: number
  application_id: number
  env: Environment
  route_key: string
  host: string
  path_prefix: string
  strip_prefix: boolean
  backend_path: string
  enabled: boolean
  backend_mode: GatewayBackendMode
  instance_select_mode: GatewayInstanceSelectMode
  instance_header: string
  sort_order: number
  backends?: ApplicationGatewayRouteBackend[]
}

export interface ApplicationInstanceHealth {
  target_id: number
  status: string
  last_checked_at?: string
  consecutive_failures: number
  consecutive_successes: number
  last_error?: string
}

export interface ApplicationEnvTarget {
  id: number
  application_id: number
  env: Environment
  instance_name: string
  display_name: string
  machine_id: number
  port: number
  enabled: boolean
  health_check_enabled: boolean
  health_check_type: string
  health_check_scheme: string
  health_check_host: string
  health_check_url: string
  health_check_path: string
  health_check_method: string
  health_check_expected_status: number
  health_check_timeout_ms: number
  health_check_interval_sec: number
  health_check_failure_threshold: number
  health_check_success_threshold: number
  gateway_routes?: ApplicationGatewayRoute[]
  health?: ApplicationInstanceHealth | null
}

export interface CreateApplicationRequest {
  name: string
  repo_url: string
  deploy_method: DeployMethod
  deploy_config?: string
  description?: string
  targets?: ApplicationEnvTargetRequest[]
}

export type UpdateApplicationRequest = Partial<CreateApplicationRequest>

export interface ApplicationGatewayRouteBackendRequest {
  target_id: number
  weight: number
  enabled?: boolean
}

export interface ApplicationGatewayRouteRequest {
  id?: number
  route_key: string
  host: string
  path_prefix: string
  strip_prefix: boolean
  backend_path?: string
  enabled?: boolean
  backend_mode: GatewayBackendMode
  instance_select_mode: GatewayInstanceSelectMode
  instance_header?: string
  backends: ApplicationGatewayRouteBackendRequest[]
  sort_order?: number
}

export interface ApplicationEnvTargetRequest {
  env: Environment
  instance_name: string
  display_name?: string
  machine_id: number
  port: number
  enabled?: boolean
  health_check_enabled?: boolean
  health_check_path?: string
  gateway_routes?: ApplicationGatewayRouteRequest[]
}

export interface Machine {
  id: number
  name: string
  machine_type: Environment
  host: string
  port: number
  user: string
  auth_type: AuthType
  ssh_key_path?: string
  workspace_root: string
  mode: MachineMode
  agent_base_url: string
  agent_proxy_base_url: string
  agent_last_heartbeat_at?: string | null
  agent_version: string
  created_at: string
  updated_at: string
  online: boolean
}

export interface CreateMachineRequest {
  name: string
  machine_type: Environment
  host?: string
  port?: number
  user?: string
  auth_type?: AuthType
  ssh_key_path?: string
  password?: string
  workspace_root?: string
  mode?: MachineMode
  agent_base_url?: string
  agent_proxy_base_url?: string
  agent_token?: string
}

export type UpdateMachineRequest = Partial<CreateMachineRequest>

// Returned once by createMachine/rotateMachineToken when agenda generates or
// rotates an agent_token — it cannot be recovered afterwards.
export interface MachineCreateResult {
  machine: Machine
  agent_token?: string
}

export interface RotateTokenResult {
  agent_token: string
}

export interface ApplicationRelease {
  id: number
  application_id: number
  env: Environment
  instance_name: string
  machine_id: number
  branch: string
  commit_sha: string
  previous_release_id: number
  deploy_log_id: number
  status: ReleaseStatus
  operator: string
  deployed_at?: string | null
  verified_at?: string | null
  created_at: string
  updated_at: string
}

export interface CreateReleaseRequest {
  env: Environment
  instance_name?: string
  branch: string
  commit_sha?: string
  operator?: string
}

export interface PipelineStep {
  id: number
  deploy_log_id: number
  name: string
  type: string
  status: string
  output: string
  error_msg: string
  started_at?: string | null
  finished_at?: string | null
}

export interface DeployLog {
  id: number
  application_id: number
  status: string
  output: string
  error_msg: string
  started_at?: string | null
  finished_at?: string | null
  steps?: PipelineStep[]
}

export interface Setting {
  id: number
  key: string
  value: string
  type: SettingType
  is_secret: boolean
  updated_by: number
  created_at: string
  updated_at: string
}

export interface UpsertSettingRequest {
  value: string
  type: SettingType
  is_secret: boolean
}

export interface User {
  id: number
  username: string
  display_name: string
  role: string
  is_active: boolean
  created_at: string
  updated_at: string
}

export interface CreateUserRequest {
  username: string
  password: string
  display_name?: string
  role: string
}

export interface NodeLogFile {
  service: string
  file: string
  lines: string[]
}

export interface NodeLogsResponse {
  app: string
  instance: string
  logs: NodeLogFile[]
}

export interface ListResponse<T> {
  data: T[]
  total: number
}

export type NotificationLevel = 'info' | 'warning' | 'critical'

export interface Notification {
  id: number
  title: string
  content: string
  level: NotificationLevel
  is_read: boolean
  created_at: string
  updated_at: string
}
