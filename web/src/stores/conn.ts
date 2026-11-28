/**
 * 连接与环境：任务状态、服务端事实、网络诊断、ASN 与地理。
 *
 * 这四块同源——都是「此刻连着的这个后端是什么样」，都在连接建立后拉一次。
 * 放在一个模块里是为了改一处就能看到全部，而不是为了共用一份 state：它们各自
 * 是独立的 store，名字与职责都不混。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { CMD } from '@/api/protocol'
import { sendCommand } from '@/api/client'
import { api } from '@/api/rest'
import type { ProgressPayload, TaskDonePayload } from '@/api/protocol'
import type {
  AuthStatus,
  DiagExportResult,
  DiagReport,
  Funnel,
  GeoStatus,
  ServerStatus,
  SourceStatus,
  TaskState,
} from '@/api/types'
import type { ConnectionState } from '@/api/ws'

/** 空闲快照。首连之前用它占位，避免各处判空。 */
function idleState(): TaskState {
  return {
    phase: 'idle',
    status: 'idle',
    done: 0,
    total: 0,
    elapsed_s: 0,
    eta_s: 0,
    funnel: { generated: 0, latency_ok: 0, region_ok: 0, usable: 0 },
    started_at: 0,
  }
}

/**
 * 一次任务的结局。
 *
 * 只由终止事件写入：state 只说「失败了」，而「卡在哪、跑了多久、有没有
 * 需要提醒的问题」只在终止事件的载荷里。下一次任务开始时清空，这样界面
 * 不会把上次的结论一直挂在上面。
 */
export interface TaskOutcome {
  status: 'done' | 'failed'
  stage: string
  durationMs: number
  warnings: string[]
}

/** 进度事件的载荷：比 state 更频繁，运行期间的界面全读它。 */
export interface TaskProgress {
  done: number
  total: number
  eta: number
  elapsed: number
  funnel: Funnel | null
}

/**
 * 把进度载荷里的漏斗转成快照的形状。
 *
 * 后端这几个字段都是数字；形状不对就当没有——拿脏值去渲染，漏斗上出现的
 * 是 NaN，比空着更难判断。
 */
function toFunnel(raw: unknown): Funnel | null {
  if (!raw || typeof raw !== 'object') return null
  const value = raw as Record<string, unknown>
  const keys = ['generated', 'latency_ok', 'region_ok', 'usable'] as const
  for (const key of keys) {
    if (typeof value[key] !== 'number') return null
  }
  return {
    generated: value.generated as number,
    latency_ok: value.latency_ok as number,
    region_ok: value.region_ok as number,
    usable: value.usable as number,
  }
}

export const useTaskStore = defineStore('task', () => {
  const connection = ref<ConnectionState>('connecting')
  const retryInSeconds = ref(0)
  const state = ref<TaskState>(idleState())
  /** 进度事件的载荷：比 state 更频繁，只用于运行期间的实时展示。 */
  const progress = ref<TaskProgress | null>(null)
  /** 上一次任务的结局；新一轮开始时清空。 */
  const outcome = ref<TaskOutcome | null>(null)

  const running = computed(() => state.value.status === 'running')
  const phase = computed(() => state.value.phase)

  /** 运行中的实时进度；不在运行或还没收到进度帧时为 null，界面回落到快照。 */
  const live = computed(() => (running.value ? progress.value : null))

  const liveDone = computed(() => live.value?.done ?? state.value.done)
  const liveTotal = computed(() => live.value?.total ?? state.value.total)
  const liveEta = computed(() => live.value?.eta ?? state.value.eta_s)
  const liveElapsed = computed(() => live.value?.elapsed ?? state.value.elapsed_s)

  const funnel = computed<Funnel>(() => live.value?.funnel ?? state.value.funnel)

  const percent = computed(() => {
    const total = liveTotal.value
    if (total <= 0) return 0
    return Math.min(100, Math.round((liveDone.value / total) * 100))
  })

  function applyState(next: TaskState): void {
    state.value = next
    // 新任务开始了：上一轮的结论不再成立，别让它挂在这一轮上面。
    if (next.status === 'running') outcome.value = null
    // 任务结束时清掉进度事件的残留，否则进度条会停在最后一帧。
    if (next.status !== 'running') progress.value = null
  }

  /**
   * 写入任务结局。
   *
   * 字段名保持后端的 snake_case：中间多一层转换，新字段就会在转换表里
   * 悄悄丢掉，而这类丢失在界面上只表现为「那块没显示」。
   */
  function applyOutcome(status: TaskOutcome['status'], payload: TaskDonePayload): void {
    outcome.value = {
      status,
      stage: payload.stage ?? '',
      durationMs: payload.duration_ms ?? 0,
      warnings: payload.warnings ?? [],
    }
  }

  function applyProgress(payload: Partial<ProgressPayload>): void {
    progress.value = {
      done: payload.done ?? 0,
      total: payload.total ?? 0,
      eta: payload.eta ?? 0,
      elapsed: payload.elapsed ?? 0,
      funnel: toFunnel(payload.funnel),
    }
  }

  function setConnection(next: ConnectionState, retrySeconds = 0): void {
    connection.value = next
    retryInSeconds.value = retrySeconds
  }

  return {
    connection,
    retryInSeconds,
    state,
    progress,
    outcome,
    running,
    phase,
    funnel,
    percent,
    liveDone,
    liveTotal,
    liveEta,
    liveElapsed,
    applyState,
    applyProgress,
    applyOutcome,
    setConnection,
  }
})

export const useSystemStore = defineStore('system', () => {
  /** 内置官方网段的段数。0 表示还没拿到。 */
  const officialV4 = ref(0)
  const officialV6 = ref(0)
  const server = ref<ServerStatus | null>(null)
  const auth = ref<AuthStatus>({ password_set: false, legacy: false })
  /** 正在重启：连接马上会断，界面要给出「在重启」而不是「掉线了」。 */
  const restarting = ref(false)

  function refresh(): void {
    sendCommand(CMD.sourceStatus)
    sendCommand(CMD.serverStatus)
    sendCommand(CMD.authStatus)
  }

  function applySource(status: SourceStatus): void {
    officialV4.value = status.official_v4
    officialV6.value = status.official_v6
  }

  function applyServer(status: ServerStatus): void {
    server.value = status
  }

  function applyAuth(status: AuthStatus): void {
    auth.value = status
  }

  /**
   * 重启程序。
   *
   * 回执先到、连接后断，所以这里只负责把按钮置为「重启中」；真正的刷新由
   * 重连后的 refresh 完成。
   */
  function restart(): boolean {
    restarting.value = true
    return sendCommand(CMD.serverRestart, {})
  }

  /**
   * 设置访问密码。
   *
   * 明文只走这一趟：服务端收到就加盐哈希，之后再也拿不回来。所以界面上不做
   * 「回填已设的密码」这种事——它做不到，也不该做。
   */
  function setPassword(password: string): boolean {
    return sendCommand(CMD.authSetPassword, { password })
  }

  return {
    officialV4,
    officialV6,
    server,
    auth,
    restarting,
    refresh,
    applySource,
    applyServer,
    applyAuth,
    restart,
    setPassword,
  }
})

export const useDiagStore = defineStore('diag', () => {
  const report = ref<DiagReport | null>(null)
  /** 诊断进行中。四项检查最坏要十几秒，必须让用户看见「在跑」。 */
  const running = ref(false)
  const exporting = ref(false)

  function run(): boolean {
    running.value = true
    const ok = sendCommand(CMD.diagRun, {})
    if (!ok) running.value = false
    return ok
  }

  function applyReport(next: DiagReport): void {
    report.value = next
    running.value = false
  }

  /**
   * 导出诊断包。
   *
   * 把手上这份报告一起发过去：服务端不留诊断结果，导出的是用户此刻看到的
   * 那一份。没跑过诊断也能导，包里会写明「本次没有跑诊断」。
   */
  function exportBundle(): boolean {
    exporting.value = true
    const ok = sendCommand(CMD.diagExport, { report: report.value })
    if (!ok) exporting.value = false
    return ok
  }

  function applyExport(result: DiagExportResult): void {
    exporting.value = false
    if (result.url) api.download(result.url, result.name)
  }

  /** 后端报错时解除等待，否则按钮会一直转下去。 */
  function fail(): void {
    running.value = false
    exporting.value = false
  }

  return { report, running, exporting, run, applyReport, exportBundle, applyExport, fail }
})

export const useGeoStore = defineStore('geo', () => {
  const status = ref<GeoStatus | null>(null)
  /** 用户点过「不再提示」之后本次会话不再显示。 */
  const warningDismissed = ref(false)

  const loaded = computed(() => status.value?.status.loaded ?? false)
  const quickFilters = computed(() => status.value?.quick_filters ?? [])
  const warning = computed(() => {
    if (warningDismissed.value) return null
    return status.value?.warning ?? null
  })

  function apply(next: GeoStatus): void {
    const previousCountry = status.value?.warning?.country
    status.value = next
    // 换了一条新的提示就重新显示：上一次的「不再提示」只对那一条生效。
    if (next.warning?.country !== previousCountry) {
      warningDismissed.value = false
    }
  }

  function dismissWarning(): void {
    warningDismissed.value = true
  }

  return { status, loaded, quickFilters, warning, apply, dismissWarning }
})
