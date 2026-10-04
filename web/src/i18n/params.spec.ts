/**
 * 档位与面板参数之间的换算。
 *
 * 这里守的是往返一致：面板上的值存成档位、再切回来必须是原来那组。换算表写错
 * 一个键时不会报错，只会让某几个参数悄悄对不上——而「档位切回去不是原来那组
 * 值」这种事，用户很难说清是哪里不对。
 */
import { describe, expect, it } from 'vitest'

import {
  CUSTOM_PRESET,
  PARAM_PATHS,
  SCAN_PARAMS,
  effectiveMax,
  matchPreset,
  paramNameOf,
  paramPaths,
  presetValues,
} from './params'

/** 后端形状的档位：值是配置里的点号路径。 */
const fast = {
  id: 'fast',
  values: {
    'scan.sample_max': 500,
    'scan.workers': 100,
    'scan.latency_threshold': 300,
    'scan.ping_times': 1,
    'speed.target_qualified': 5,
  },
}

const standard = {
  id: 'standard',
  values: {
    'scan.sample_max': 2000,
    'scan.workers': 150,
    'scan.latency_threshold': 230,
    'speed.target_qualified': 10,
  },
}

const presets = [fast, standard]

describe('presetValues', () => {
  it('把点号路径换算成面板参数名', () => {
    expect(presetValues(fast)).toEqual({
      sampleMax: 500,
      workers: 100,
      latencyThreshold: 300,
      pingTimes: 1,
    })
  })

  it('丢掉面板上没有的字段', () => {
    // 测速数不属于这个面板，硬塞进来会让面板显示一堆改不了的项。
    expect(presetValues(fast)).not.toHaveProperty('targetQualified')
    expect(presetValues(fast)).not.toHaveProperty('speed.target_qualified')
  })

  it('档位不存在时给一个空对象，而不是抛错', () => {
    expect(presetValues(undefined)).toEqual({})
  })
})

describe('matchPreset', () => {
  it('参数与档位声明的一致时命中该档位', () => {
    const values = { sampleMax: 500, workers: 100, latencyThreshold: 300, pingTimes: 1 }
    expect(matchPreset(values, presets)).toBe('fast')
  })

  it('只看档位声明过、且面板上有的键', () => {
    // 面板上多出来的项（用户自己调的其它参数）不该让判定失败。
    const values = { sampleMax: 2000, workers: 150, latencyThreshold: 230, timeoutMs: 800 }
    expect(matchPreset(values, presets)).toBe('standard')
  })

  it('档位里带的测速参数不参与面板的判定', () => {
    // fast 与 standard 的 speed.target_qualified 不同，但面板上改不了它，
    // 因此判定只看扫描那几项。
    const values = { sampleMax: 500, workers: 100, latencyThreshold: 300, pingTimes: 1 }
    expect(matchPreset(values, presets)).toBe('fast')
  })

  it('对不上任何档位时落到自定义', () => {
    const values = { sampleMax: 777, workers: 100, latencyThreshold: 300, pingTimes: 1 }
    expect(matchPreset(values, presets)).toBe(CUSTOM_PRESET)
  })

  it('在面板上一个键都没声明的档位被跳过，不当作命中', () => {
    const speedOnly = { id: 'speed-only', values: { 'speed.concurrency': 4 } }
    const values = { sampleMax: 500, workers: 100, latencyThreshold: 300, pingTimes: 1 }
    expect(matchPreset(values, [speedOnly, ...presets])).toBe('fast')
  })

  it('档位列表为空时落到自定义', () => {
    expect(matchPreset({ workers: 100 }, [])).toBe(CUSTOM_PRESET)
  })

  it('参数缺一项时不算命中：不能拿缺省值硬凑', () => {
    // fast 声明了 ping_times=1，面板上却没有这一项，说明参数被改过。
    const values = { sampleMax: 500, workers: 100, latencyThreshold: 300 }
    expect(matchPreset(values, presets)).toBe(CUSTOM_PRESET)
  })
})

describe('paramPaths', () => {
  it('把面板参数换算成点号路径，并丢掉面板上没有的字段', () => {
    const values = { sampleMax: 500, workers: 100, timeoutMs: 800 }
    expect(paramPaths(values)).toEqual({
      'scan.sample_max': 500,
      'scan.workers': 100,
      'scan.timeout_ms': 800,
    })
  })

  it('布尔值也要带上', () => {
    expect(paramPaths({ twoPhase: false })).toEqual({ 'scan.two_phase': false })
  })
})

describe('effectiveMax', () => {
  it('没有联动时用声明里的上限', () => {
    const spec = SCAN_PARAMS.find((item) => item.key === 'pingTimes')!
    expect(effectiveMax(spec, {})).toBe(spec.max)
  })

  /**
   * 并发那一项的上限跟着「并发上限」走。
   *
   * 面板上改的值会写回配置，所以上限必须跟着配置里那个值走——不跟的话面板能填
   * 出一个必定被拒的值，而写回失败是静默的：用户改了、看起来生效了，其实没存。
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

describe('往返一致', () => {
  it('面板参数存成档位再切回来是同一组值', () => {
    const values = {
      sampleMax: 500,
      workers: 100,
      latencyThreshold: 300,
      pingTimes: 1,
      port: 443,
      timeoutMs: 1000,
      retry: 0,
      twoPhase: true,
      verifyNodes: false,
    }
    expect(presetValues({ values: paramPaths(values) })).toEqual(values)
  })

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
