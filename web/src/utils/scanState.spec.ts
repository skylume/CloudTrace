/**
 * 扫描页参数的取值来源。
 *
 * 这里守的是「首次运行」这条判定：判错一次，用户刚选的档位就会被当成「从没
 * 设置过的默认值」，自适应随即把它改掉——而用户只会看到自己的选择莫名其妙
 * 变了。
 */
import { describe, expect, it } from 'vitest'

import { PARAM_PATHS, PARAM_WIRE_KEYS } from '@/i18n/params'

import { diffScanParams, isUntouchedScan, scanParamsFromConfig } from './scanState'

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

describe('diffScanParams', () => {
  it('只挑出改动过的键', () => {
    const { patch, origins } = diffScanParams({ workers: 60, sampleMax: 500 }, { workers: 100, sampleMax: 500 })

    expect(patch).toEqual({ workers: 60 })
    expect(origins).toEqual({ 'scan.workers': 'user' })
  })

  it('一个都没改时给空补丁，调用方据此不发请求', () => {
    const { patch, origins } = diffScanParams({ workers: 100 }, { workers: 100 })
    expect(patch).toEqual({})
    expect(origins).toEqual({})
  })

  /**
   * 整组写会把用户没碰过的项也标成 user，自适应从此再也不能动它们。
   * 这里守住「只写改动过的」。
   */
  it('没碰过的项不会被打上 user 标记', () => {
    const { origins } = diffScanParams(
      { workers: 60, sampleMax: 500, latencyThreshold: 300 },
      { workers: 100, sampleMax: 500, latencyThreshold: 300 },
    )
    expect(Object.keys(origins)).toEqual(['scan.workers'])
  })

  it('还没定下来的项不写回去', () => {
    // 档位列表与配置都还没到时面板是空的，这时写回等于凭空造值。
    const { patch } = diffScanParams({}, { workers: 100 })
    expect(patch).toEqual({})
  })

  it('false 与 0 是有效改动，不能被当成缺省', () => {
    const { patch, origins } = diffScanParams({ twoPhase: false, retry: 0 }, { twoPhase: true, retry: 2 })
    expect(patch).toEqual({ two_phase: false, retry: 0 })
    expect(origins).toEqual({ 'scan.two_phase': 'user', 'scan.retry': 'user' })
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
