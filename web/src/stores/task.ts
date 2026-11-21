/**
 * 任务状态：连接状态、当前任务快照、实时进度。
 *
 * 状态来源只有一处：后端的 `state` 事件（首连与每次变更全量下发）。进度事件
 * 只用来做更细的刷新，不参与状态判断——否则断线重连之后进度与状态会对不上。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import type { TaskDonePayload } from '@/api/protocol'
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
    preset: '',
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

export const useTaskStore = defineStore('task', () => {
  const connection = ref<ConnectionState>('connecting')
  const retryInSeconds = ref(0)
  const state = ref<TaskState>(idleState())
  /** 进度事件的载荷：比 state 更频繁，只用于进度条动画。 */
  const progress = ref<{ done: number; total: number; eta: number } | null>(null)
  /** 上一次任务的结局；新一轮开始时清空。 */
  const outcome = ref<TaskOutcome | null>(null)

  const running = computed(() => state.value.status === 'running')
  const phase = computed(() => state.value.phase)
  const funnel = computed<Funnel>(() => state.value.funnel)

  const percent = computed(() => {
    const total = state.value.total
    if (total <= 0) return 0
    return Math.min(100, Math.round((state.value.done / total) * 100))
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

  function applyProgress(payload: { done?: number; total?: number; eta?: number }): void {
    progress.value = {
      done: payload.done ?? 0,
      total: payload.total ?? 0,
      eta: payload.eta ?? 0,
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
    applyState,
    applyProgress,
    applyOutcome,
    setConnection,
  }
})
