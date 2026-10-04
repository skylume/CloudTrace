/**
 * WebSocket 协议：命令名、事件名与各自的载荷。
 *
 * 命令名与事件名刻意用字符串字面量而不是枚举：它们就是后端 handlers 表里的
 * 键，改错一个字母时希望是运行时报「未注册的命令」，而不是编译期悄悄改名。
 */

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

  settingsGet: 'settings/get',
  settingsUpdate: 'settings/update',
  settingsReset: 'settings/reset',

  presetsList: 'presets/list',
  presetsApply: 'presets/apply',
  presetsSave: 'presets/save',
  presetsDelete: 'presets/delete',
  presetsSetDefault: 'presets/set_default',

  migrateStatus: 'migrate/status',
  migrateRun: 'migrate/run',

  export: 'export',
  healthCheck: 'health/check',
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
  scanDone: 'scan/done',
  scanAbort: 'scan/abort',
  speedPartial: 'speed/partial',
  /** 自适应静默调整了参数；界面要给徽标与「还原」。 */
  adaptiveApplied: 'adaptive/applied',
  /** 自适应只给建议，值未变。 */
  adaptiveSuggestion: 'adaptive/suggestion',
  /** 自动选源之后说明「为什么用了这个源」。 */
  speedSource: 'speed/source',
  /** 测速被限流熔断。带可操作的建议，与一般错误分开。 */
  speedBreaker: 'speed/breaker',
  speedDone: 'speed/done',
  speedAbort: 'speed/abort',

  historyList: 'history/list',
  historyGet: 'history/get',
  historyLoad: 'history/load',
  historyDelete: 'history/delete',
  historyUndo: 'history/undo',
  historyTag: 'history/tag',
  historyCompare: 'history/compare',
  historyChanged: 'history/changed',

  settings: 'settings',
  /** 档位列表：既是一次读取的结果，也是任何一次档位变更的广播。 */
  presets: 'presets',
  /** 旧版数据迁移：同样读与变更共用一个事件名。 */
  migrate: 'migrate',
  export: 'export',
  health: 'health',
  geo: 'geo',
} as const

export type CommandName = (typeof CMD)[keyof typeof CMD]
export type EventName = (typeof EVT)[keyof typeof EVT]

/** 错误事件的数据体。 */
export interface ErrorPayload {
  code: string
  msg: string
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
  funnel: unknown
  eta: number
}

/** 任务终止事件的载荷。 */
export interface TaskDonePayload {
  id?: string
  count: number
}

/** 历史变更事件的载荷。 */
export interface HistoryChangePayload {
  id?: string
  action: string
}
