/**
 * 档位排序。
 *
 * 这里守的是「按位置算，而不是交换两个 Order 值」：两个档位的 Order 可能相同
 * （都是新存的，默认 0），交换一个不变的值等于没动，界面上表现成「点了上移
 * 没反应」。
 */
import { describe, expect, it } from 'vitest'

import type { Preset } from '@/api/types'

import { reorderPlan } from './presets'

function preset(id: string, order: number): Preset {
  return { id, name: id, builtin: false, order, values: { 'scan.workers': 10 } }
}

/** 把计划转成「标识 → 新排序号」，断言时不受遍历顺序影响。 */
function asMap(plan: { id: string; order: number }[]): Record<string, number> {
  return Object.fromEntries(plan.map((step) => [step.id, step.order]))
}

describe('reorderPlan', () => {
  it('上移一位：被挤下去的那个也要跟着改', () => {
    const items = [preset('a', 0), preset('b', 1), preset('c', 2)]
    // 位置重排后 c 在第 1 位、b 在第 2 位；a 没动，不必重发。
    expect(asMap(reorderPlan(items, 'c', -1))).toEqual({ c: 1, b: 2 })
  })

  it('下移一位', () => {
    const items = [preset('a', 0), preset('b', 1), preset('c', 2)]
    expect(asMap(reorderPlan(items, 'a', 1))).toEqual({ b: 0, a: 1 })
  })

  it('已在首位时上移是空操作', () => {
    expect(reorderPlan([preset('a', 0), preset('b', 1)], 'a', -1)).toEqual([])
  })

  it('已在末位时下移是空操作', () => {
    expect(reorderPlan([preset('a', 0), preset('b', 1)], 'b', 1)).toEqual([])
  })

  it('认不出的标识是空操作', () => {
    expect(reorderPlan([preset('a', 0)], 'nope', 1)).toEqual([])
  })

  /**
   * 关键场景：两个档位的 Order 相同（都是新存的，默认 0）。
   *
   * 交换两个相同的值等于没动，所以必须按位置重新编号。
   */
  it('排序号相同时也能真的换位', () => {
    const items = [preset('a', 0), preset('b', 0), preset('c', 0)]
    const plan = reorderPlan(items, 'b', -1)
    // b 挪到首位后它自己仍是 0（没变，不必重发），被挤开的两个往后排。
    expect(asMap(plan)).toEqual({ a: 1, c: 2 })
    expect(plan.map((step) => step.id)).not.toContain('b')
  })

  it('顺序没变的档位不出现在计划里', () => {
    // 每个都要走一次保存，没动的不用重发。
    const items = [preset('a', 0), preset('b', 1), preset('c', 2), preset('d', 3)]
    const plan = reorderPlan(items, 'b', -1)
    expect(plan.map((step) => step.id)).not.toContain('d')
  })

  // 置顶就是「往上移到底」：delta 取当前位置的负值即可，不必另写一条路径。
  it('置顶：把末尾那个一次挪到最前', () => {
    const items = [preset('a', 0), preset('b', 1), preset('c', 2), preset('d', 3)]
    const plan = reorderPlan(items, 'd', -3)
    expect(asMap(plan)).toEqual({ d: 0, a: 1, b: 2, c: 3 })
  })

  it('已经在最前时置顶是空操作', () => {
    const items = [preset('a', 0), preset('b', 1)]
    expect(reorderPlan(items, 'a', -0)).toEqual([])
  })

  it('单个档位时任何方向都是空操作', () => {
    expect(reorderPlan([preset('a', 0)], 'a', -1)).toEqual([])
    expect(reorderPlan([preset('a', 0)], 'a', 1)).toEqual([])
  })
})
