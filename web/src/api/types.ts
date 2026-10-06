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
  /**
   * 当前任务的实时漏斗。
   *
   * 是**扁平的 Funnel**，不是 `Summary`——后端 `model.TaskState.Funnel` 就是
   * 这个形状。这里曾经写成 `Summary` 并让调用方读 `state.funnel.funnel`，
   * 于是每一次服务端 state 事件之后 `task.funnel` 都是 undefined，读它的
   * 组件直接抛错（扫描页的过程面板与结果页的漏斗条都中过）。
   */
  funnel: Funnel
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
  /** 单个目标的下载量上限（MB），0 = 不限。到量即停。 */
  max_download_mb: number
  breaker_429: number
  usability_check: boolean
  timeout_ms: number
}

/** 参数来源三态，决定自适应能不能改这个参数。 */
export type ParamOrigin = 'default' | 'preset' | 'user'
export type ParamOrigins = Record<string, ParamOrigin>

/**
 * 一个档位：一批参数值的快照。
 *
 * `values` 的键是配置里的点号路径（`scan.workers`），与 `origins` 同形——两份
 * 表用同一套键，「这一项是从哪个档位来的」才对得上。
 */
export interface Preset {
  id: string
  name: string
  note?: string
  /** 内置档位只读：不可改、不可删。 */
  builtin: boolean
  icon?: string
  color?: string
  order: number
  values: Record<string, unknown>
  origins?: ParamOrigins
}

export interface PresetsPayload {
  presets: Preset[]
  /** 启动档位的 id。 */
  default: string
}

/** 一次旧版数据迁移的结果。 */
export interface MigrateReport {
  /** 备份目录；出问题能翻回去。 */
  backup_dir?: string
  /** 配置是否已写入。 */
  settings: boolean
  /** 导入的历史份数与失败数。 */
  imported: number
  failed: number
  /** 要告诉用户的调整。 */
  notes?: string[]
  /** 没能搬过来的项及原因。 */
  skipped?: string[]
}

/** 旧版数据迁移的状态。 */
export interface MigrateStatus {
  /** 是否检测到旧版数据。 */
  found: boolean
  /** 旧数据所在目录。 */
  dir?: string
  /** 旧数据的构成。 */
  settings: boolean
  histories: number
  /** 这份旧数据是否已经迁移过。 */
  migrated: boolean
  /** 最近一次迁移的结果。 */
  report?: MigrateReport
}

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

/** 两份历史的对比结果。 */
export interface HistoryDiff {
  added_count: number
  removed_count: number
  changed_count: number
  unchanged_count: number
  avg_latency_a: number
  avg_latency_b: number
  latency_delta: number
  best_changed: boolean
  best_a: string
  best_b: string
  /** 各地区数量的变化（B 减 A），只列出有变化的部分。 */
  region_delta: Record<string, number>
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
  /** 正在下载库文件。 */
  downloading: boolean
  /** 已读取的字节数。 */
  download_read: number
  /** 总字节数；0 表示服务端没给长度，此时只显示「进行中」。 */
  download_total: number
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
  /** 高对比度：与深浅正交的可访问性开关。 */
  contrast: boolean
  remember_state: boolean
  adaptive_enabled: boolean
  adaptive_allow_preset: boolean
  /** 关闭窗口时收进托盘（桌面版）。 */
  close_to_tray: boolean
}

/**
 * 通知渠道与触发条件。
 *
 * 两个维度是正交的：`on_done` / `on_fail` 决定「这次结束值不值得提醒」，
 * `web` / `sound` / `tray` 决定「用什么提醒」。
 */
export interface NotifyConfig {
  /** 托盘通知（桌面版）。 */
  tray: boolean
  /** 浏览器通知，需要授权。 */
  web: boolean
  /** 任务完成时提醒。 */
  on_done: boolean
  /** 任务失败时提醒。 */
  on_fail: boolean
  /** 提示音。 */
  sound: boolean
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
  notify?: NotifyConfig
  advanced?: Record<string, unknown>
  origins?: ParamOrigins
}

export interface SettingsPayload {
  values: Settings
  restart_required?: string[]
  /**
   * 值合法但可能带来麻烦的项（并发过高、阈值过低之类）。
   *
   * 随配置一起下发，界面就地标警示色——代价要在改的那一刻说清楚，而不是等
   * 用户自己想起来去点「配置体检」。
   */
  warnings?: FieldIssue[]
}

/** 一条针对具体配置项的提示。 */
export interface FieldIssue {
  key: string
  value?: unknown
  reason: string
}

/** 一项网络检查的结论。 */
export type DiagStatus = 'ok' | 'warn' | 'bad'

/** 一项网络检查。 */
export interface DiagItem {
  /** dns | tcp | trace | egress，与后端 diag.Key* 一致。 */
  key: string
  status: DiagStatus
  /** 「出了什么事」，一句话。 */
  summary: string
  /** 原始数据（耗时、地址、trace 字段）。 */
  detail?: string
  /** 「怎么办」。只在不是 ok 时给。 */
  advice?: string
}

/** 一次网络诊断的完整结果。 */
export interface DiagReport {
  checked_at: string
  /** 总体结论：各项里最差的那个。 */
  status: DiagStatus
  items: DiagItem[]
  elapsed_ms: number
}

/** 诊断包的下载地址。 */
export interface DiagExportResult {
  id: string
  url: string
  name: string
}
