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
export type EnvDeploymentStatus = 'pending' | 'running' | 'success' | 'partial_failed' | 'failed'
export type SettingType = 'string' | 'int' | 'bool' | 'json'
export type GatewayBackendMode = 'single' | 'all_enabled' | 'selected'
export type GatewayInstanceSelectMode = 'disabled' | 'enabled'
// Whether the gateway may turn a request on this route into a protocol upgrade.
// 'none' (the default) rejects Upgrade requests outright.
export type GatewayUpgradeMode = 'none' | 'websocket'

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
  upgrade_mode: GatewayUpgradeMode
  // Ordinary HTTP total timeout; 0 = the gateway's default. Never applied to a
  // WebSocket — a total deadline on a tunnel is a scheduled disconnect, not a
  // timeout — which is what websocket_idle_timeout_ms is for.
  request_timeout_ms: number
  // 0 = gateway default, negative = no idle timeout (app must Ping to stay honest).
  websocket_idle_timeout_ms: number
  // 0 = unlimited for this route (the gateway-wide cap still applies).
  websocket_max_connections: number
  // Comma-separated Origin allowlist for browser handshakes; empty = any origin.
  websocket_allowed_origins: string
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
  // 'running' | 'stopped' — the operator's runtime intent, orthogonal to
  // `enabled`. A decommissioned instance is 'stopped': its containers are torn
  // down and it is drained from the gateway, but the record survives so it can
  // be brought back. Rows predating this field read as 'running'.
  desired_state?: RuntimeState
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
  metrics_enabled: boolean
  metrics_port: number
  gateway_routes?: ApplicationGatewayRoute[]
  health?: ApplicationInstanceHealth | null
}

export type RuntimeState = 'running' | 'stopped'

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
  upgrade_mode?: GatewayUpgradeMode
  request_timeout_ms?: number
  websocket_idle_timeout_ms?: number
  websocket_max_connections?: number
  websocket_allowed_origins?: string
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
  metrics_enabled?: boolean
  metrics_port?: number
  gateway_routes?: ApplicationGatewayRouteRequest[]
}

// EnvVarMatrix is one application's env vars for every environment. The
// backend always returns an entry per environment (empty object when nothing is
// configured), so the console can render a Key x (prod, stage, test) matrix
// without inferring which environments exist.
export type EnvVarMatrix = Record<Environment, Record<string, string>>

export interface ApplicationEnvironments {
  application_id: number
  envs: EnvVarMatrix
}

// Environments omitted from `envs` are left untouched; an environment present
// with an empty object has its vars cleared. There is no inheritance between
// environments: a blank value is stored as an empty string.
export interface UpdateApplicationEnvironmentsRequest {
  envs: Partial<EnvVarMatrix>
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
  // Resolved against the global config's workspace_root when the machine does
  // not set its own. This is the only directory agenda may write to on the
  // machine, and the fixed prefix of any upload path.
  effective_workspace_root: string
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
  env_deployment_id: number
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

export interface EnvDeployment {
  id: number
  application_id: number
  env: Environment
  branch: string
  commit_sha: string
  operator: string
  status: EnvDeploymentStatus
  total_count: number
  success_count: number
  failed_count: number
  started_at: string
  finished_at?: string | null
  releases?: ApplicationRelease[]
}

export interface CreateEnvDeploymentRequest {
  env: Environment
  // Empty/omitted = deploy every enabled instance of the env; set = just that one.
  instance_name?: string
  branch?: string
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

export type FileScope = 'app_env' | 'machine'
export type FileVerifyStatus = '' | 'ok' | 'changed' | 'missing' | 'unreachable'

// One upload of one file to one machine. Uploads append rows rather than
// replacing them, so a list is a history; `current` marks the row that
// describes what is on disk now.
export interface MachineFile {
  id: number
  scope: FileScope
  application_id: number
  app_name: string
  env: Environment | ''
  machine_id: number
  machine_name: string
  path: string
  file_name: string
  size: number
  sha256: string
  mode: string
  user_id: number
  username: string
  created_at: string
  last_verified_at: string | null
  last_verify_status: FileVerifyStatus
  last_verify_sha256: string
  last_verify_error: string
  current: boolean
}

// An environment upload reaches several machines and any of them can fail on
// its own, so the response reports each separately.
export interface FileUploadResult {
  machine_id: number
  machine_name: string
  path: string
  success: boolean
  error?: string
  file?: MachineFile
}

export interface UploadEnvFileResponse {
  data: FileUploadResult[]
  container_path: string
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

export type AlertLevel = 'info' | 'warning' | 'critical'
export type AlertRuleState = 'ok' | 'firing'

// Never carries webhook_url/secret — see internal/handler.listAlertChannels.
export interface AlertChannel {
  name: string
  type: string
  enabled: boolean
}

export interface AlertRule {
  id: number
  name: string
  expr: string
  for_seconds: number
  level: AlertLevel
  channels: string[]
  enabled: boolean
  state: AlertRuleState
  consecutive_breaches: number
  last_evaluated_at?: string | null
  last_fired_at?: string | null
  last_error: string
  created_at: string
  updated_at: string
}

export interface UpsertAlertRuleRequest {
  name: string
  expr: string
  for_seconds: number
  level: AlertLevel
  channels: string[]
  enabled?: boolean
}

export interface AlertRuleTestResult {
  firing: boolean
  result?: unknown
  error?: string
}

export type DatabaseEngine = 'mysql' | 'redis'

// A registered database always lives on its machine — there is no host field.
// agenda-node connects to it locally, so the port is never published.
export interface DatabaseInstance {
  id: number
  name: string
  engine: DatabaseEngine
  machine_id: number
  port: number
  username: string
  default_database: string
  env: Environment
  description: string
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface CreateDatabaseInstanceRequest {
  name: string
  engine?: DatabaseEngine
  machine_id: number
  port: number
  username: string
  password?: string
  default_database?: string
  env?: Environment
  description?: string
  enabled?: boolean
}

export type UpdateDatabaseInstanceRequest = Partial<CreateDatabaseInstanceRequest>

export interface DatabaseColumn {
  name: string
  type: string
  // Set when the column held bytes that are not valid UTF-8; those cells are
  // base64-encoded rather than shown raw.
  binary: boolean
}

// A cell is null for SQL NULL, which is deliberately distinct from ''.
export type DatabaseCell = string | null

export interface QueryResult {
  columns: DatabaseColumn[]
  rows: DatabaseCell[][]
  row_count: number
  truncated: boolean
  duration_ms: number
  database: string
  query_log_id: number
}

export interface QueryRequest {
  database?: string
  sql: string
  max_rows?: number
  timeout_ms?: number
}

// A Redis reply comes back in the same QueryResult shape a SQL result does, so
// the grid and the history viewer stay one implementation. db is optional
// because omitting it means "the instance's registered default index" — which
// 0 does not, 0 being a real index.
export interface RedisCommandRequest {
  db?: number
  command: string
  max_rows?: number
  timeout_ms?: number
}

// How many numeric databases the server has, for the console's DB picker.
export interface RedisDatabaseCount {
  count: number
}

export interface DBQueryLog {
  id: number
  instance_id: number
  instance_name: string
  env: Environment
  user_id: number
  username: string
  database_name: string
  statement: string
  result_truncated: boolean
  row_count: number
  duration_ms: number
  success: boolean
  error: string
  created_at: string
}

// Only a single-entry read carries the stored result; a listing never does.
export interface DBQueryLogDetail extends DBQueryLog {
  result?: {
    columns: DatabaseColumn[]
    rows: DatabaseCell[][]
    truncated: boolean
  }
}

export interface TestDatabaseInstanceResult {
  ok: boolean
  server_version?: string
  error?: string
}

// Every table in a schema with its column names, in one call — the shape the
// editor's completion needs. Best-effort: on a very large schema this can be
// slow or truncated, and the console degrades to completion without columns.
export interface SchemaOutline {
  tables: Record<string, string[]>
}
