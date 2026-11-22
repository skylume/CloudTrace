/**
 * 扫描页参数的取值来源。
 *
 * 这里守的是「首次运行」这条判定：判错一次，用户刚选的档位就会被当成「从没
 * 设置过的默认值」，自适应随即把它改掉——而用户只会看到自己的选择莫名其妙
 * 变了。
 */
import { describe, expect, it } from 'vitest'

import { PARAM_PATHS, PARAM_WIRE_KEYS } from '@/i18n/params'

import { isUntouchedScan, scanParamsFromConfig } from './scanState'

describe('isUntouchedScan', () => {
  it('来源表为空时算没设置过', () => {
    expect(isUntouchedScan(undefined)).toBe(true)
    expect(isUntouchedScan({})).toBe(true)
  })

  it('整组标记也算设置过', () => {
    // 恢复默认支持整组粒度（只写 `scan`），只认单参数标记的话会把它误判成
    // 「从没设置过」，于是下次进扫描页又被填一遍启动档位。
    expect(isUntouchedScan({ scan: 'user' })).toBe(false)
  })

  it('单个参数标记算设置过', () => {
    expect(isUntouchedScan({ 'scan.workers': 'user' })).toBe(false)
    expect(isUntouchedScan({ 'scan.workers': 'preset' })).toBe(false)
  })

  it('别的分组的标记不影响判定', () => {
    expect(isUntouchedScan({ 'ui.theme': 'user' })).toBe(true)
    expect(isUntouchedScan({ 'speed.concurrency': 'user' })).toBe(true)
  })

  it('前缀相像但不属于扫描分组的键不算', () => {
    // `scanx` 不是 `scan` 的子键，别被 startsWith 骗过去。
    expect(isUntouchedScan({ scanx: 'user' })).toBe(true)
  })
})

describe('scanParamsFromConfig', () => {
  it('把下划线命名的配置值换算成界面参数', () => {
    const got = scanParamsFromConfig({
      sample_max: 500,
      workers: 100,
      latency_threshold: 300,
      ping_times: 1,
      port: 443,
      two_phase: true,
    })
    expect(got).toEqual({
      sampleMax: 500,
      workers: 100,
      latencyThreshold: 300,
      pingTimes: 1,
      port: 443,
      twoPhase: true,
    })
  })

  it('丢掉面板上没有的键', () => {
    const got = scanParamsFromConfig({ workers: 100, custom_source: 'x', mode: 'tcping' })
    expect(got).toEqual({ workers: 100 })
  })

  it('非标量值不参与，也不塞一个 0 进去', () => {
    // 0 是有意义的取值（如重试次数），拿它当「缺省」会让界面显示一个假数字。
    const got = scanParamsFromConfig({ workers: '100', pre_filter_ports: [443] })
    expect(got).toEqual({})
  })

  it('配置为空时给一个空对象', () => {
    expect(scanParamsFromConfig(undefined)).toEqual({})
  })

  it('布尔 false 也要带上，不能被当成缺省', () => {
    expect(scanParamsFromConfig({ two_phase: false })).toEqual({ twoPhase: false })
  })
})

describe('PARAM_WIRE_KEYS', () => {
  it('与配置路径一一对应', () => {
    for (const [name, path] of Object.entries(PARAM_PATHS)) {
      expect(PARAM_WIRE_KEYS[name], `${name} 没有对应的载荷字段`).toBe(path.split('.').pop())
    }
    expect(Object.keys(PARAM_WIRE_KEYS)).toHaveLength(Object.keys(PARAM_PATHS).length)
  })
})
