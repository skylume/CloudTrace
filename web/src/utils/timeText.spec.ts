/**
 * 时间显示。
 *
 * 这里守两件事：相对时间不出现负数与「0 分钟前」，绝对时间的格式不随运行环境
 * 的区域设置变——它是拿去和日志对照的。
 */
import { describe, expect, it } from 'vitest'

import { ageOf, formatAbsolute, hasAge } from './timeText'

/** 一个固定的「现在」：2026-10-04T12:00:00Z。 */
const NOW = Date.UTC(2026, 9, 4, 12, 0, 0)

describe('ageOf', () => {
  it('一分钟内算刚刚', () => {
    expect(ageOf(new Date(NOW - 20_000).toISOString(), NOW)).toEqual({ unit: 'justNow', value: 0 })
  })

  it('分钟、小时、天各一档', () => {
    expect(ageOf(new Date(NOW - 12 * 60_000).toISOString(), NOW)).toEqual({ unit: 'minutes', value: 12 })
    expect(ageOf(new Date(NOW - 3 * 3600_000).toISOString(), NOW)).toEqual({ unit: 'hours', value: 3 })
    expect(ageOf(new Date(NOW - 50 * 3600_000).toISOString(), NOW)).toEqual({ unit: 'days', value: 2 })
  })

  it('边界：59 分钟仍是分钟，60 分钟进小时', () => {
    expect(ageOf(new Date(NOW - 59 * 60_000).toISOString(), NOW).unit).toBe('minutes')
    expect(ageOf(new Date(NOW - 60 * 60_000).toISOString(), NOW).unit).toBe('hours')
  })

  /**
   * 未来时间不显示负数。
   *
   * 时钟偏差或后端时间略快都会出现这种情况，而「-3 分钟前」看起来像程序坏了。
   */
  it('未来时间按刚刚处理', () => {
    expect(ageOf(new Date(NOW + 60_000).toISOString(), NOW)).toEqual({ unit: 'justNow', value: 0 })
  })

  it('认不出的时间不编一个相对值出来', () => {
    expect(hasAge('不是时间')).toBe(false)
    expect(hasAge('2026-10-04T12:00:00Z')).toBe(true)
  })
})

describe('formatAbsolute', () => {
  it('本地模式给出本地时间', () => {
    // 只断言形状与日期，具体小时数取决于运行环境的时区。
    const got = formatAbsolute('2026-10-04T12:34:00Z', 'local')
    expect(got).toMatch(/^2026-10-0[45] \d{2}:\d{2}$/)
  })

  it('UTC 模式与输入一致，并带后缀', () => {
    expect(formatAbsolute('2026-10-04T12:34:00Z', 'utc')).toBe('2026-10-04 12:34 UTC')
  })

  it('补零到两位', () => {
    expect(formatAbsolute('2026-01-02T03:04:00Z', 'utc')).toBe('2026-01-02 03:04 UTC')
  })

  it('认不出的时间原样返回，不显示 Invalid Date', () => {
    expect(formatAbsolute('不是时间', 'utc')).toBe('不是时间')
  })
})
