/**
 * WebSocket 协议：命令名、事件名与各自的载荷。
 *
 * 命令名与事件名刻意用字符串字面量而不是枚举：它们就是后端 handlers 表里的
 * 键，改错一个字母时希望是运行时报「未注册的命令」，而不是编译期悄悄改名。
 */
import type { Funnel, IPRecord, StageStat } from './types'

/** 客户端 → 服务端：命令。 */
export const CMD = {
  ping: 'ping',

  scanStart: 'scan/start',
  scanStop: 'scan/stop',
  speedStart: 'speed/start',
  speedStop: 'speed/stop',

  historyList: 'history/list',
  historyGet: 'history/get',
  historyLoad: 'history/load',
  historyDelete: 'history/delete',
  historyUndo: 'history/undo',
  historyTag: 'history/tag',
  historyCompare: 'history/compare',
  historyClear: 'history/clear',

  settingsGet: 'settings/get',
  settingsUpdate: 'settings/update',
  settingsReset: 'settings/reset',

  migrateStatus: 'migrate/status',
  migrateRun: 'migrate/run',

  /* 档位已下线：只读出旧 presets.json，导入与导出都由前端发起。 */
  legacyPresets: 'presets/legacy',
  legacyPresetsImport: 'presets/legacy/import',

  export: 'export',
  healthCheck: 'health/check',
  sourceStatus: 'source/status',
  serverStatus: 'server/status',
  serverRestart: 'server/restart',
  authStatus: 'auth/status',
  authSetPassword: 'auth/set-password',
  diagRun: 'diag/run',
  diagExport: 'diag/export',
  geoStatus: 'geo/status',
  geoUpdate: 'geo/update',
} as const

/** 服务端 → 客户端：事件。 */
export const EVT = {
  state: 'state',
  pong: 'pong',
  error: 'error',

  progress: 'progress',
  scanResult: 'scan/result',
  /*
   * 完成与中止事件的 topic 由阶段名拼出（`scan` + `/done`）。
   * 内置界面不用它们——完成与否看 `state` 的 status 就够了——但外部脚本
   * 靠它们拿结果条数，所以协议里留着。
   */
  scanDone: 'scan/done',
  scanAbort: 'scan/abort',
  /* 失败：载荷与完成事件同形，额外带阶段、耗时与告警——「卡在哪」全靠它。 */
  scanFailed: 'scan/failed',
  /** 一条都没达标时补发的「最接近阈值」那批，用来判断是不是条件太严。 */
  scanNearest: 'scan/nearest',
  speedDone: 'speed/done',
  speedAbort: 'speed/abort',
  speedFailed: 'speed/failed',
  speedPartial: 'speed/partial',
  /** 自适应静默调整了参数；界面要给徽标与「还原」。 */
  adaptiveApplied: 'adaptive/applied',
  /** 自适应只给建议，值未变。 */
  adaptiveSuggestion: 'adaptive/suggestion',
  /** 自动选源之后说明「为什么用了这个源」。 */
  speedSource: 'speed/source',
  /** 测速被限流熔断。带可操作的建议，与一般错误分开。 */
  speedBreaker: 'speed/breaker',
  /** 内置来源的段数。 */
  source: 'source',
  /** 面板的可访问地址与鉴权状态。 */
  server: 'server',
  /** 重启命令的回执（连接随后会断开）。 */
  serverRestart: 'server/restart',
  /** 访问密码的现状。 */
  auth: 'auth',
  /** 网络诊断结果。 */
  diag: 'diag',
  /** 诊断包的下载地址。 */
  diagExport: 'diag/export',

  historyList: 'history/list',
  historyGet: 'history/get',
  historyLoad: 'history/load',
  historyDelete: 'history/delete',
  historyUndo: 'history/undo',
  historyTag: 'history/tag',
  historyCompare: 'history/compare',
  historyClear: 'history/clear',
  historyChanged: 'history/changed',

  settings: 'settings',
  /** 旧版数据迁移：同样读与变更共用一个事件名。 */
  migrate: 'migrate',
  /** 旧档位库还在不在，以及刚导入的结果。 */
  legacyPresets: 'presets/legacy',
  export: 'export',
  health: 'health',
  geo: 'geo',
} as const

export type CommandName = (typeof CMD)[keyof typeof CMD]
export type EventName = (typeof EVT)[keyof typeof EVT]

/** 零结果回看：本次的延迟上限与最接近它的若干条探测结果。 */
export interface NearestPayload {
  threshold: number
  items: IPRecord[]
}

/** 错误事件的数据体。 */
export interface ErrorPayload {
  code: string
  msg: string
}

/** 错误码。前端据它决定表现：Toast / 跳登录 / 字段高亮。 */
export const ERROR_CODE = {
  busy: 'E_BUSY',
  invalidParam: 'E_INVALID_PARAM',
  notFound: 'E_NOT_FOUND',
  unauthorized: 'E_UNAUTHORIZED',
  io: 'E_IO',
  network: 'E_NETWORK',
  asnUnavailable: 'E_ASN_UNAVAILABLE',
  unknown: 'E_UNKNOWN',
} as const

/** 报文格式：客户端发命令、服务端发事件，用的是同一个外壳。 */
export interface Envelope<T = unknown> {
  type: string
  data?: T
}

/** 进度事件的载荷。 */
export interface ProgressPayload {
  phase: string
  done: number
  total: number
  funnel: Funnel
  eta: number
  /** 已耗时，秒。运行期间 state 不重发，计时器只能读这里。 */
  elapsed: number
  /** 流水线各阶段的统计；旧后端不带这个字段。 */
  stages?: StageStat[]
}

/** 任务终止事件的载荷。 */
export interface TaskDonePayload {
  id?: string
  count: number
  /** 失败/结束所在阶段，取值与 ProgressPayload.phase 一致。 */
  stage?: string
  /** 本阶段耗时，毫秒。 */
  duration_ms?: number
  /** 不致命、但值得说一声的问题。 */
  warnings?: string[]
}

/** 历史变更事件的载荷。 */
export interface HistoryChangePayload {
  id?: string
  action: string
}
