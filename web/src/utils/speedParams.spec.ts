/**
 * 测速参数的组装。
 *
 * 这里守的是一条很容易静默失效的接线：`speed/start` 收的是**完整参数**，
 * 后端不会替调用方读配置。曾经只发 `{scope, targets}`，于是设置页里那些
 * 测速项一个都没生效——界面照常改、照常保存，任务跑的还是默认值。
 */
import { describe, expect, it } from 'vitest'

import type { IPRecord, Settings } from '@/api/types'

import { buildSpeedParams, inferIPVersion } from './speedParams'

function target(ip: string): IPRecord {
  return {
    ip,
    port: 443,
    use_tls: true,
    latency: 42,
    latency_avg: 42,
    latency_max: 45,
    jitter: 1,
    loss: 0,
    sent: 4,
    recv: 4,
    colo: 'HKG',
    loc: 'HK',
    region_name: '中国香港',
    speed_mbps: 0,
    score: 0,
  }
}

/** 只带我们关心的分组，其余交给类型断言。 */
function settingsWith(groups: Partial<Settings>): Settings {
  return { ui: {} as Settings['ui'], ...groups }
}

describe('inferIPVersion', () => {
  it('全是 v6 目标时归到 6', () => {
    expect(inferIPVersion([target('2606:4700::1'), target('2606:4700::2')])).toBe(6)
  })

  it('混着 v4 时归到 4', () => {
    expect(inferIPVersion([target('2606:4700::1'), target('1.1.1.1')])).toBe(4)
  })

  it('没有目标时给一个确定的值，不返回 0', () => {
    expect(inferIPVersion([])).toBe(4)
  })
})

describe('buildSpeedParams', () => {
  it('把配置里的测速项原样带上', () => {
    const settings = settingsWith({
      speed: {
        url_mode: 'mobile_only',
        custom_url: 'https://example.com/down',
        concurrency: 4,
        target_qualified: 25,
        interval_ms: 800,
        min_speed: 6.5,
        weight_speed: 2,
        weight_latency: 0.5,
        weight_jitter: 0.2,
        per_region_topn: 3,
        download_duration_s: 15,
        breaker_429: 5,
      },
      net: { use_tls: 'true' },
      scan: { usability_check: false },
    })

    const params = buildSpeedParams(settings, { scope: 'single', targets: [target('1.1.1.1')] })

    expect(params).toMatchObject({
      scope: 'single',
      ip_version: 4,
      url_mode: 'mobile_only',
      custom_url: 'https://example.com/down',
      use_tls: 'true',
      concurrency: 4,
      target_qualified: 25,
      interval_ms: 800,
      min_speed: 6.5,
      weight_speed: 2,
      weight_latency: 0.5,
      weight_jitter: 0.2,
      per_region_topn: 3,
      download_duration_s: 15,
      breaker_429: 5,
      usability_check: false,
    })
  })

  it('配置还没到时落到后端默认值，而不是发出 undefined', () => {
    const params = buildSpeedParams(null, { scope: 'single', targets: [target('1.1.1.1')] })

    expect(params.url_mode).toBe('auto')
    expect(params.use_tls).toBe('auto')
    expect(params.concurrency).toBe(1)
    expect(params.interval_ms).toBe(1200)
    expect(params.breaker_429).toBe(3)
    expect(params.usability_check).toBe(true)
    // 0 是「交给后端按默认超时决定」，不是漏填。
    expect(params.timeout_ms).toBe(0)
  })

  it('权重里的 0 是有效值，不能被当成没设置', () => {
    const settings = settingsWith({
      speed: { weight_jitter: 0, min_speed: 0, per_region_topn: 0 },
    })

    const params = buildSpeedParams(settings, { scope: 'single', targets: [target('1.1.1.1')] })

    expect(params.weight_jitter).toBe(0)
    expect(params.min_speed).toBe(0)
    expect(params.per_region_topn).toBe(0)
  })

  it('目标原样透传，不做任何改写', () => {
    const targets = [target('1.1.1.1'), target('1.0.0.1')]
    const params = buildSpeedParams(null, { scope: 'all', targets })

    expect(params.targets).toBe(targets)
  })
})
