/**
 * 参数联动校验。
 *
 * 这里守的是「只提示、不自动改」这条边界，以及两个入口读的是同一张规则表：
 * 设置页给的是嵌套配置，扫描页给的是扁平驼峰参数，一旦两边的映射走散，
 * 就会出现「设置页提示了、扫描页不提示」这种只有用户能发现的不一致。
 */
import { describe, expect, it } from 'vitest'

import { checkCombinations, checkScanParams } from './paramRules'

describe('checkCombinations（设置页：嵌套配置）', () => {
  it('并发高又不留间隔时命中测速规则', () => {
    const warnings = checkCombinations({ speed: { concurrency: 8, interval_ms: 200 } })

    expect(warnings.map((item) => item.rule)).toEqual(['speed_concurrency_vs_interval'])
    expect(warnings[0]?.keys).toEqual(['speed.concurrency', 'speed.interval_ms'])
    expect(warnings[0]?.values).toEqual([8, 200])
  })

  it('间隔够长就不提示', () => {
    expect(checkCombinations({ speed: { concurrency: 8, interval_ms: 1000 } })).toEqual([])
  })

  it('并发不高就不提示，哪怕间隔很短', () => {
    expect(checkCombinations({ speed: { concurrency: 5, interval_ms: 1 } })).toEqual([])
  })

  it('并发高而超时短时命中扫描规则', () => {
    const warnings = checkCombinations({ scan: { workers: 200, timeout_ms: 300 } })

    expect(warnings.map((item) => item.rule)).toEqual(['scan_workers_vs_timeout'])
    expect(warnings[0]?.values).toEqual([200, 300])
  })

  it('阈值很紧而采样很少时命中采样规则', () => {
    const warnings = checkCombinations({ scan: { latency_threshold: 60, sample_max: 200 } })

    expect(warnings.map((item) => item.rule)).toEqual(['threshold_vs_sample'])
  })

  it('采样为 0（不限制）不算采得少', () => {
    expect(checkCombinations({ scan: { latency_threshold: 60, sample_max: 0 } })).toEqual([])
  })

  it('多条规则可以同时命中，顺序与规则表一致', () => {
    const warnings = checkCombinations({
      speed: { concurrency: 10, interval_ms: 100 },
      scan: { workers: 200, timeout_ms: 300, latency_threshold: 60, sample_max: 200 },
    })

    expect(warnings.map((item) => item.rule)).toEqual([
      'speed_concurrency_vs_interval',
      'scan_workers_vs_timeout',
      'threshold_vs_sample',
    ])
  })

  it('参数缺一个就整条不判断，不会拿缺省值硬凑', () => {
    // 只有并发没有间隔：不能假设间隔是 0，那样等于凭空造出一个问题。
    expect(checkCombinations({ speed: { concurrency: 16 } })).toEqual([])
    expect(checkCombinations({})).toEqual([])
  })

  it('非数字的取值不参与判断', () => {
    expect(checkCombinations({ speed: { concurrency: '8', interval_ms: 200 } })).toEqual([])
    expect(checkCombinations({ speed: { concurrency: Number.NaN, interval_ms: 200 } })).toEqual([])
  })
})

describe('checkScanParams（扫描页：扁平驼峰参数）', () => {
  it('与设置页读到同一组规则', () => {
    const flat = checkScanParams({ workers: 200, timeoutMs: 300 })
    const nested = checkCombinations({ scan: { workers: 200, timeout_ms: 300 } })

    expect(flat).toEqual(nested)
  })

  it('阈值与采样的驼峰键也能对上', () => {
    const warnings = checkScanParams({ latencyThreshold: 60, sampleMax: 200 })

    expect(warnings.map((item) => item.rule)).toEqual(['threshold_vs_sample'])
    expect(warnings[0]?.keys).toEqual(['scan.latency_threshold', 'scan.sample_max'])
    expect(warnings[0]?.values).toEqual([60, 200])
  })

  it('扫描页里没有测速参数，测速规则不会误命中', () => {
    expect(checkScanParams({ workers: 100, timeoutMs: 1000 })).toEqual([])
  })
})
