/**
 * 延迟分档与格式化。
 *
 * 这里覆盖的是「看不出错、但显示就是不对」的地方：0 被当成「没有数据」、
 * 边界值落错档、不可达显示成 0。这些错误在界面上都不显眼，却会让人做出
 * 错误的判断。
 */
import { describe, expect, it } from 'vitest'

import { UNREACHABLE } from '@/api/types'

import { formatLatency, formatLoss, formatSpeed, latencyTier, tierBars, tierColorVar } from './latency'

describe('latencyTier 分档', () => {
  it('按文档给定的边界分档', () => {
    expect(latencyTier(42.3)).toBe('fastest')
    expect(latencyTier(99.9)).toBe('fastest')
    expect(latencyTier(100)).toBe('fast')
    expect(latencyTier(179.9)).toBe('fast')
    expect(latencyTier(180)).toBe('mid')
    expect(latencyTier(249.9)).toBe('mid')
    expect(latencyTier(250)).toBe('slow')
    expect(latencyTier(999)).toBe('slow')
  })

  it('0 是合法的极快值，不能被当成没有数据', () => {
    expect(latencyTier(0)).toBe('fastest')
    expect(tierBars(latencyTier(0))).toBe(4)
  })

  it('哨兵值与非法值按不可达处理', () => {
    expect(latencyTier(UNREACHABLE)).toBe('dead')
    expect(latencyTier(-1)).toBe('dead')
    expect(latencyTier(Number.NaN)).toBe('dead')
    expect(latencyTier(Number.POSITIVE_INFINITY)).toBe('dead')
  })
})

describe('信号格数', () => {
  it('格数随档位递减，不可达为 0 格', () => {
    expect(tierBars('fastest')).toBe(4)
    expect(tierBars('fast')).toBe(3)
    expect(tierBars('mid')).toBe(2)
    expect(tierBars('slow')).toBe(1)
    expect(tierBars('dead')).toBe(0)
  })

  it('颜色变量都在主题里定义', () => {
    for (const tier of ['fastest', 'fast', 'mid', 'slow', 'dead'] as const) {
      expect(tierColorVar(tier)).toMatch(/^var\(--latency-(fast|mid|slow|dead)\)$/)
    }
    // 极快与快共用一档颜色，靠格数区分。
    expect(tierColorVar('fastest')).toBe(tierColorVar('fast'))
  })
})

describe('格式化', () => {
  it('不可达显示破折号而不是 0', () => {
    expect(formatLatency(UNREACHABLE)).toBe('—')
    expect(formatLatency(-1)).toBe('—')
    expect(formatLatency(0)).toBe('0.0')
  })

  it('延迟保留指定小数位', () => {
    expect(formatLatency(42.34)).toBe('42.3')
    expect(formatLatency(42.34, 2)).toBe('42.34')
  })

  it('未测速的速度给破折号，0 也算未测', () => {
    expect(formatSpeed(0)).toBe('—')
    expect(formatSpeed(-1)).toBe('—')
    expect(formatSpeed(14.83)).toBe('14.8')
  })

  it('没有样本时丢包率给破折号，而不是 0%', () => {
    expect(formatLoss(0, 0)).toBe('—')
    expect(formatLoss(0, 3)).toBe('0%')
    expect(formatLoss(1, 3)).toBe('100%')
    expect(formatLoss(1 / 3, 3)).toBe('33%')
  })
})
