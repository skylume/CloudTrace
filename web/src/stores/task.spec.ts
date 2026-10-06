/**
 * 任务状态的接线。
 *
 * 这个文件只钉一件事：**服务端 state 事件的形状**。`funnel` 曾经被前端当成
 * `Summary` 读（`state.funnel.funnel`），而后端一直是扁平的 `Funnel`——
 * 每次 state 事件之后 `task.funnel` 都变成 undefined，读它的组件直接抛错。
 * 类型对齐查不出这种问题（两边各自自洽），所以这里按后端真实载荷断言。
 */
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'

import type { TaskState } from '@/api/types'
import { useTaskStore } from './task'

/** 与后端 `model.TaskState` 的 json 形状一致。 */
function serverState(patch: Partial<TaskState> = {}): TaskState {
  return {
    phase: 'scan',
    status: 'running',
    done: 120,
    total: 500,
    elapsed_s: 8.5,
    eta_s: 27,
    funnel: { generated: 500, latency_ok: 342, region_ok: 318, usable: 8 },
    preset: '标准',
    started_at: 1_700_000_000,
    ...patch,
  }
}

describe('task store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('空闲时漏斗是零值，不是 undefined', () => {
    const task = useTaskStore()
    expect(task.funnel).toEqual({ generated: 0, latency_ok: 0, region_ok: 0, usable: 0 })
  })

  it('收到 state 事件后能直接读到漏斗的四个数', () => {
    const task = useTaskStore()
    task.applyState(serverState())

    expect(task.funnel.generated).toBe(500)
    expect(task.funnel.latency_ok).toBe(342)
    expect(task.funnel.region_ok).toBe(318)
    expect(task.funnel.usable).toBe(8)
  })

  it('进度按已完成数算，总数未知时不显示 100%', () => {
    const task = useTaskStore()
    task.applyState(serverState({ done: 250, total: 500 }))
    expect(task.percent).toBe(50)

    task.applyState(serverState({ done: 0, total: 0 }))
    expect(task.percent).toBe(0)
  })

  it('running 只看 status，不看阶段', () => {
    const task = useTaskStore()
    task.applyState(serverState({ phase: 'speed', status: 'running' }))
    expect(task.running).toBe(true)

    task.applyState(serverState({ phase: 'speed', status: 'done' }))
    expect(task.running).toBe(false)
  })
})
