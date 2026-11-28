import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

import type { TaskState } from '@/api/types'
import { useTaskStore } from './conn'

function runningState(extra: Partial<TaskState> = {}): TaskState {
  return {
    phase: 'scan',
    status: 'running',
    done: 0,
    total: 0,
    elapsed_s: 0,
    eta_s: 0,
    funnel: { generated: 0, latency_ok: 0, region_ok: 0, usable: 0 },
    started_at: 0,
    ...extra,
  }
}

describe('任务阶段统计', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('进度载荷带阶段时读进度', () => {
    const task = useTaskStore()
    task.applyState(runningState())
    task.applyProgress({
      done: 10,
      total: 10,
      stages: [{ name: 'probe', total: 10, ok: 8, failed: 2, done: 10 }],
    })

    expect(task.stages).toHaveLength(1)
    expect(task.stages[0]?.name).toBe('probe')
    expect(task.stages[0]?.ok).toBe(8)
  })

  it('进度不带阶段时回退到快照', () => {
    const task = useTaskStore()
    task.applyState(
      runningState({
        stages: [{ name: 'pool', total: 100, ok: 80, failed: 0, skipped: 20, done: 100 }],
      }),
    )
    task.applyProgress({ done: 1, total: 2 })

    expect(task.stages).toHaveLength(1)
    expect(task.stages[0]?.name).toBe('pool')
    expect(task.stages[0]?.skipped).toBe(20)
  })

  it('形状不对的阶段项被跳过，合法项保留', () => {
    const task = useTaskStore()
    task.applyState(runningState())
    task.applyProgress({
      stages: [
        { name: 'probe', total: 10, ok: 9, failed: 1, done: 10 },
        { name: 42, total: 1, ok: 1, failed: 0, done: 1 },
        { name: 'measure', total: 'x', ok: 1, failed: 0, done: 1 },
      ] as never,
    })

    expect(task.stages).toHaveLength(1)
    expect(task.stages[0]?.name).toBe('probe')
  })

  it('任务结束后清掉进度，回落到快照阶段', () => {
    const task = useTaskStore()
    task.applyState(
      runningState({
        stages: [{ name: 'collect', total: 5, ok: 5, failed: 0, done: 5 }],
      }),
    )
    task.applyProgress({ done: 5, total: 5, stages: [{ name: 'probe', total: 5, ok: 5, failed: 0, done: 5 }] })
    task.applyState(runningState({ status: 'done', stages: [{ name: 'collect', total: 5, ok: 5, failed: 0, done: 5 }] }))

    expect(task.stages[0]?.name).toBe('collect')
  })
})
