/**
 * 质量两轴的判定。
 *
 * 这里守两件事：同一行在两个视图里不能给出互相矛盾的结论；0ms 与不可达
 * 哨兵不能混为一谈。两类错误在界面上都不显眼，却会让人做出错误判断。
 */
import { describe, expect, it } from 'vitest'

import { UNREACHABLE, type IPRecord } from '@/api/types'

import { qualityOf, qualityRank, reachabilityOf, reachable, speedVerdictOf } from './quality'

function record(patch: Partial<IPRecord> = {}): IPRecord {
  return {
    ip: '1.1.1.1',
    port: 443,
    use_tls: true,
    latency: 42,
    latency_avg: 42,
    latency_max: 42,
    jitter: 1,
    loss: 0,
    sent: 4,
    recv: 4,
    colo: 'HKG',
    loc: 'HK',
    region_name: '香港',
    speed_mbps: 0,
    score: 0,
    ...patch,
  }
}

describe('可达性', () => {
  it('收到回包且延迟不是哨兵才算可达', () => {
    expect(reachable(record())).toBe(true)
    expect(reachable(record({ recv: 0 }))).toBe(false)
    expect(reachable(record({ latency: UNREACHABLE }))).toBe(false)
  })

  it('0ms 是合法的极快值，不是不可达', () => {
    expect(reachabilityOf(record({ latency: 0 }))).toBe('reachable')
  })
})

describe('测速结论', () => {
  it('没测出速度是未测速，不是不合格', () => {
    expect(speedVerdictOf(record({ speed_mbps: 0 }), 5)).toBe('untested')
  })

  it('合格线未读到时，测出速度即合格', () => {
    expect(speedVerdictOf(record({ speed_mbps: 1 }), 0)).toBe('qualified')
  })

  it('低于合格线判不合格', () => {
    expect(speedVerdictOf(record({ speed_mbps: 4.9 }), 5)).toBe('below')
    expect(speedVerdictOf(record({ speed_mbps: 5 }), 5)).toBe('qualified')
  })
})

describe('视图标记', () => {
  it('结果视图只看可达性', () => {
    expect(qualityOf(record(), false, 5)).toEqual({ tone: 'muted', label: 'quality.reachable' })
    expect(qualityOf(record({ recv: 0 }), false, 5)).toEqual({ tone: 'bad', label: 'quality.unreachable' })
  })

  it('测速视图按速度三态给色', () => {
    expect(qualityOf(record({ speed_mbps: 8 }), true, 5)).toEqual({ tone: 'ok', label: 'quality.qualified' })
    expect(qualityOf(record({ speed_mbps: 2 }), true, 5)).toEqual({ tone: 'warn', label: 'quality.below' })
    expect(qualityOf(record({ speed_mbps: 0 }), true, 5)).toEqual({ tone: 'muted', label: 'quality.untested' })
  })
})

describe('排序等级', () => {
  it('合格 > 低于阈值 > 未测速 > 不可达', () => {
    expect(qualityRank(record({ speed_mbps: 8 }), 5)).toBe(3)
    expect(qualityRank(record({ speed_mbps: 2 }), 5)).toBe(2)
    expect(qualityRank(record({ speed_mbps: 0 }), 5)).toBe(1)
    expect(qualityRank(record({ recv: 0 }), 5)).toBe(0)
  })
})
