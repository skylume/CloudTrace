/**
 * 任务状态：连接状态、当前任务快照、实时进度。
 *
 * 状态来源只有一处：后端的 `state` 事件（首连与每次变更全量下发）。进度事件
 * 只用来做更细的刷新，不参与状态判断——否则断线重连之后进度与状态会对不上。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

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

export const useTaskStore = defineStore('task', () => {
  const connection = ref<ConnectionState>('connecting')
  const retryInSeconds = ref(0)
  const state = ref<TaskState>(idleState())
  /** 进度事件的载荷：比 state 更频繁，只用于进度条动画。 */
  const progress = ref<{ done: number; total: number; eta: number } | null>(null)

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
    // 任务结束时清掉进度事件的残留，否则进度条会停在最后一帧。
    if (next.status !== 'running') progress.value = null
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
    running,
    phase,
    funnel,
    percent,
    applyState,
    applyProgress,
    setConnection,
  }
})
