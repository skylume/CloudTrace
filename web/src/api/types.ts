/**
 * 与后端一一对应的数据模型。
 *
 * 字段名就是 Go struct 的 json tag，**不做任何转换**——两端各写一份映射表
 * 是「字段悄悄漂移」的根源。后端改了字段，这里跟着改，类型检查会兜住漏改
 * 的地方。
 *
 * 唯一不在本文档里的来源是后端的 `GET /api/export/fields`：导出字段清单由
 * 后端下发，前端不硬编码。
 */

/** 延迟哨兵值：探测全失败时延迟字段的取值。0 是合法的极快延迟，两者必须区分。 */
export const UNREACHABLE = -1

export interface IPRecord {
  ip: string
  port: number
  use_tls: boolean

  latency: number
  latency_avg: number
  latency_max: number
  /** 样本标准差，单位毫秒。 */
  jitter: number
  /** 0..1 */
  loss: number
  sent: number
  recv: number

  /** 数据中心 IATA 代码。 */
  colo: string
  /** 出口国家码。 */
  loc: string
  /** 地区中文名，由后端翻译。 */
  region_name: string
  asn?: number
  as_org?: string
  geo_warn?: string

  speed_mbps: number
  score: number

  /** 节点明细，仅在开启明细采集时存在。 */
  trace?: Record<string, string>
}

export interface Funnel {
  generated: number
  latency_ok: number
  region_ok: number
  usable: number
}

export interface Summary {
  funnel: Funnel
  region_dist: Record<string, number>
  asn_dist: Record<string, number>
  min_latency: number
  avg_latency: number
  best_speed: number
  avg_speed: number
  qualified: number
  total: number
}

export type TaskPhase = 'idle' | 'scan' | 'speed'
export type TaskStatus = 'idle' | 'running' | 'done' | 'aborted' | 'failed'

export interface TaskState {
  phase: TaskPhase
  status: TaskStatus
  done: number
  total: number
  elapsed_s: number
  eta_s: number
  funnel: Summary
  preset: string
  started_at: number
  error?: string
}

export interface ScanParams {
  mode: string
  workers: number
  sample_max: number
  latency_threshold: number
  ping_times: number
  port: number
  ip_version: number
  source_mode: string
  custom_source: string
  pre_filter_ports: number[]
  allowed_regions: string[]
  blocked_regions: string[]
  two_phase: boolean
  verify_nodes: boolean
  timeout_ms: number
  retry: number
}

export interface SpeedParams {
  scope: string
  targets: IPRecord[]
  ip_version: number
  url_mode: string
  custom_url: string
  use_tls: string
  concurrency: number
  target_qualified: number
  interval_ms: number
  min_speed: number
  weight_speed: number
  weight_latency: number
  weight_jitter: number
  per_region_topn: number
  download_duration_s: number
  breaker_429: number
  usability_check: boolean
  timeout_ms: number
}

/** 参数来源三态，决定自适应能不能改这个参数。 */
export type ParamOrigin = 'default' | 'preset' | 'user'
export type ParamOrigins = Record<string, ParamOrigin>

export interface HistoryRecord {
  id: string
  type: 'scan' | 'speed'
  ip_version: number
  created_at: string
  duration_s: number
  preset?: string
  /** 参数快照，内容按 type 决定是 ScanParams 还是 SpeedParams。 */
  params: unknown
  origins?: ParamOrigins
  summary: Summary
  count: number
  tags?: string[]
  note?: string
  starred: boolean
  results: IPRecord[]
}

export interface HistoryIndexEntry {
  id: string
  type: 'scan' | 'speed'
  ip_version: number
  created_at: string
  duration_s?: number
  preset?: string
  count: number
  tags?: string[]
  note?: string
  starred: boolean
  best_speed?: number
  min_latency?: number
  regions?: string[]
  params_hash?: string
}

export interface ParamDiff {
  key: string
  label: string
  from: string
  to: string
}

/** 体检发现。 */
export interface HealthIssue {
  key: string
  level: 'warning' | 'error'
  problem: string
  suggestion: string
  field?: string
  fixable: boolean
  fix?: { key: string; value: unknown; label: string }
}

export interface HealthReport {
  issues: HealthIssue[]
  checked: number
}

/** 导出字段定义，由后端下发。 */
export interface FieldDef {
  key: string
  label: string
  type: 'string' | 'int' | 'float' | 'bool' | 'object'
  group: string
  default: boolean
}

export interface FieldPreset {
  id: string
  name: string
  keys: string[]
}

export interface ExportFields {
  fields: FieldDef[]
  presets: FieldPreset[]
  formats: string[]
}

export interface ExportResult {
  id: string
  url: string
  name: string
  count: number
  total: number
}

export interface ASNStatus {
  source: string
  path: string
  file_time: string
  records: number
  updated_at: string
  loaded: boolean
  error?: string
}

export interface GeoWarning {
  country: string
  message: string
}

export interface GeoStatus {
  status: ASNStatus
  warning?: GeoWarning
  quick_filters: string[]
}

/** 界面设置。字段与后端 Config 的 ui.* 一致。 */
export interface UIConfig {
  theme: 'dark' | 'light' | 'system'
  lang: 'zh' | 'en'
  font_scale: 'small' | 'medium' | 'large'
  density: 'auto' | 'simple' | 'advanced'
  table_density: 'compact' | 'normal' | 'comfortable'
  page_size: number
  time_format: 'local' | 'utc'
  start_page: string
  animation: boolean
  remember_state: boolean
  adaptive_enabled: boolean
  adaptive_allow_preset: boolean
}

/**
 * 全量配置。
 *
 * 这里只声明前端会读会写的几组；其余分组按 `Record<string, unknown>` 透传，
 * 前端不认识的键一个字节都不改（写回时原样带上）。
 */
export interface Settings {
  scan?: Record<string, unknown>
  speed?: Record<string, unknown>
  source?: Record<string, unknown>
  net?: Record<string, unknown>
  geo?: Record<string, unknown>
  history?: Record<string, unknown>
  data?: Record<string, unknown>
  export?: Record<string, unknown>
  ui: UIConfig
  server?: Record<string, unknown>
  notify?: Record<string, unknown>
  advanced?: Record<string, unknown>
  origins?: ParamOrigins
}

export interface SettingsPayload {
  values: Settings
  restart_required?: string[]
}
