/**
 * 任务状态：连接状态、当前任务快照、实时进度。
 *
 * 权威状态只有一处：后端的 `state` 事件（首连与每次变更全量下发）。但运行
 * 期间后端只按节流发 `progress`、不重发 `state`——所以进度条、计时器、剩余
 * 时间与漏斗都必须读实时值。只读快照的话，整场任务会停在 0，结束时直接跳满。
 *
 * 实时值只在 running 时覆盖快照，任务一结束就回到快照：断线重连之后进度与
 * 状态不会对不上。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import type { ProgressPayload, TaskDonePayload } from '@/api/protocol'
import type { Funnel, TaskState } from '@/api/types'
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
