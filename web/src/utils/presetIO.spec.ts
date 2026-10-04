/**
 * 档位的导入导出。
 *
 * 这里守的是「往返一致」与「坏文件不能悄悄吞掉」：导入是唯一一个把外部数据
 * 放进配置的入口，而放进去的东西会被当成参数直接用于扫描。
 */
import { describe, expect, it } from 'vitest'

import type { Preset } from '@/api/types'

import { exportPresets, parsePresets, uniquePresetID } from './presetIO'

const office: Preset = {
  id: 'office',
  name: '公司网络',
  note: '上行窄',
  builtin: false,
  order: 2,
  values: { 'scan.workers': 60, 'scan.latency_threshold': 260 },
}

const builtinFast: Preset = {
  id: 'fast',
  name: '快速',
  builtin: true,
  order: 0,
  values: { 'scan.workers': 100 },
}

describe('exportPresets', () => {
  it('只导出自定义档位', () => {
    // 内置档位跟着文件跑到别人机器上只会变成一份过期的拷贝，还顶着内置的名字。
    const parsed = JSON.parse(exportPresets([builtinFast, office])) as { presets: Preset[] }
    expect(parsed.presets.map((item) => item.id)).toEqual(['office'])
  })

  it('导出物能被自己读回来', () => {
    const round = parsePresets(exportPresets([office]))
    expect(round).toHaveLength(1)
    expect(round[0]?.name).toBe('公司网络')
    expect(round[0]?.note).toBe('上行窄')
    expect(round[0]?.values).toEqual({ 'scan.workers': 60, 'scan.latency_threshold': 260 })
    expect(round[0]?.builtin).toBe(false)
  })
})

describe('parsePresets', () => {
  it('也接受一个裸的档位数组', () => {
    // 手写或从别处复制来的常见形态，为它单独报错没有意义。
    const round = parsePresets(JSON.stringify([{ name: '手写', values: { 'scan.workers': 10 } }]))
    expect(round).toHaveLength(1)
    expect(round[0]?.name).toBe('手写')
  })

  it('不是 JSON 时明确报错', () => {
    expect(() => parsePresets('{ not json')).toThrow('不是合法的 JSON')
  })

  it('没有档位列表时报错', () => {
    expect(() => parsePresets('{"version":1}')).toThrow('文件里没有档位列表')
    expect(() => parsePresets('"hello"')).toThrow('文件里没有档位列表')
  })

  it('空列表报错，而不是导入 0 个后说成功', () => {
    expect(() => parsePresets('{"presets":[]}')).toThrow('档位列表是空的')
  })

  it('缺少名称时报错并指出是第几项', () => {
    expect(() => parsePresets(JSON.stringify([{ name: 'ok', values: { a: 1 } }, { values: { a: 1 } }]))).toThrow(
      '第 2 项没有名称',
    )
  })

  it('参数值不是对象时报错', () => {
    expect(() => parsePresets(JSON.stringify([{ name: 'x', values: [1, 2] }]))).toThrow('第 1 项没有参数值')
    expect(() => parsePresets(JSON.stringify([{ name: 'x' }]))).toThrow('第 1 项没有参数值')
  })

  it('参数值为空对象时报错', () => {
    // 一个不含参数的档位存下来也没用，早点说清楚。
    expect(() => parsePresets(JSON.stringify([{ name: 'x', values: {} }]))).toThrow('参数值是空的')
  })

  it('不认识的字段被丢掉，只保留认识的', () => {
    const round = parsePresets(JSON.stringify([{ name: 'x', values: { a: 1 }, 恶意字段: 'x' }]))
    expect(round[0]).not.toHaveProperty('恶意字段')
  })

  it('内置标记被强制清掉', () => {
    // 文件里写着 builtin 也不能让导入的档位变成只读的内置档位。
    const round = parsePresets(JSON.stringify([{ name: 'x', values: { a: 1 }, builtin: true }]))
    expect(round[0]?.builtin).toBe(false)
  })
})

describe('uniquePresetID', () => {
  it('不冲突时原样使用', () => {
    expect(uniquePresetID('office', new Set())).toBe('office')
  })

  it('冲突时换一个，不覆盖已有的档位', () => {
    const got = uniquePresetID('office', new Set(['office']))
    expect(got).not.toBe('office')
    expect(got).toContain('office')
  })

  it('连续冲突时一直往后找', () => {
    const got = uniquePresetID('office', new Set(['office', 'office-2', 'office-3']))
    expect(got).toBe('office-4')
  })

  it('标识为空时生成一个', () => {
    expect(uniquePresetID('', new Set())).toMatch(/^imported-/)
  })
})
