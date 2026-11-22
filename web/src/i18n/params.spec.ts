/**
 * 面板参数名与配置路径之间的换算。
 *
 * 换算表写错一个键不会报错，只会让某几个参数悄悄对不上；这里守的是双向可逆。
 */
import { describe, expect, it } from 'vitest'

import { PARAM_PATHS, SCAN_PARAMS, effectiveMax, paramNameOf } from './params'

describe('effectiveMax', () => {
  it('没有联动时用声明里的上限', () => {
    const spec = SCAN_PARAMS.find((item) => item.key === 'pingTimes')!
    expect(effectiveMax(spec, {})).toBe(spec.max)
  })

  /**
   * 并发那一项的上限跟着配置里的「并发上限」走。
   *
   * 面板上填的值会随任务下发，服务端按配置里的上限校验——上限不跟的话面板能
   * 填出一个必定被拒的值。
   */
  it('并发上限跟着配置里的那一项', () => {
    const spec = SCAN_PARAMS.find((item) => item.key === 'workers')!
    expect(effectiveMax(spec, { net: { max_workers: 100 } })).toBe(100)
    expect(effectiveMax(spec, { net: { max_workers: 8000 } })).toBe(8000)
  })

  it('配置里取不到时退回声明里的值，而不是变成没有上限', () => {
    const spec = SCAN_PARAMS.find((item) => item.key === 'workers')!
    expect(effectiveMax(spec, {})).toBe(spec.max)
    expect(effectiveMax(spec, { net: { max_workers: '100' } })).toBe(spec.max)
  })
})

describe('参数名与配置路径', () => {
  it('换算表里每个键都双向对得上', () => {
    for (const [name, path] of Object.entries(PARAM_PATHS)) {
      expect(paramNameOf(path), `${path} 反查不回 ${name}`).toBe(name)
    }
  })

  it('不属于面板的路径反查为空', () => {
    expect(paramNameOf('speed.concurrency')).toBeUndefined()
    expect(paramNameOf('scan.nope')).toBeUndefined()
  })
})
