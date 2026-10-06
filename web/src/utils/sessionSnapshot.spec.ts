/**
 * 会话快照的读写。
 *
 * 这里钉的是「读回来的东西一定是可用的」——它存的是崩溃恢复用的数据，读出来
 * 半截记录比读不出来更糟：用户点了「恢复」，结果表格里出现一堆空行。
 */
import { beforeEach, describe, expect, it } from 'vitest'

import type { IPRecord } from '@/api/types'
import {
  clearSnapshot,
  loadSnapshot,
  MIN_RESTORE_RECORDS,
  saveSnapshot,
  worthRestoring,
} from './sessionSnapshot'

function record(index: number): IPRecord {
  return {
    ip: `104.16.0.${index}`,
    port: 443,
    use_tls: true,
    latency: 40 + index,
    latency_avg: 42,
    latency_max: 50,
    jitter: 2,
    loss: 0,
    sent: 4,
    recv: 4,
    colo: 'HKG',
    region_name: '中国香港',
    loc: 'HK',
    asn: 13335,
    as_org: 'CLOUDFLARENET',
    geo_warn: '',
    speed_mbps: 0,
    score: 0,
  } as IPRecord
}

describe('会话快照', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('存进去能原样读回来', () => {
    saveSnapshot({ savedAt: 1_700_000_000_000, phase: 'scan', total: 3, records: [record(1)] })

    const snap = loadSnapshot()
    expect(snap).not.toBeNull()
    expect(snap?.phase).toBe('scan')
    expect(snap?.records).toHaveLength(1)
    expect(snap?.records[0]?.ip).toBe('104.16.0.1')
  })

  it('没存过时返回 null，而不是一个空快照', () => {
    expect(loadSnapshot()).toBeNull()
  })

  it('坏数据当作没有，不抛错', () => {
    localStorage.setItem('cloudtrace.session', '{ 这不是 JSON')
    expect(loadSnapshot()).toBeNull()

    localStorage.setItem('cloudtrace.session', JSON.stringify({ savedAt: 'x', records: [] }))
    expect(loadSnapshot()).toBeNull()
  })

  it('清掉之后就读不到了', () => {
    saveSnapshot({ savedAt: 1, phase: 'scan', total: 1, records: [record(1)] })
    clearSnapshot()
    expect(loadSnapshot()).toBeNull()
  })

  it('超过上限时只保留最早的那些', () => {
    const many = Array.from({ length: 2500 }, (_, i) => record(i % 250))
    saveSnapshot({ savedAt: 1, phase: 'scan', total: many.length, records: many })

    const snap = loadSnapshot()
    expect(snap?.records).toHaveLength(2000)
    // 先扫出来的那些才是用户真正在意的，尾部被裁掉。
    expect(snap?.records[0]?.ip).toBe('104.16.0.0')
  })

  it('条数太少时不值得提示恢复', () => {
    const few = Array.from({ length: MIN_RESTORE_RECORDS - 1 }, (_, i) => record(i))
    const many = Array.from({ length: MIN_RESTORE_RECORDS }, (_, i) => record(i))

    expect(worthRestoring({ savedAt: 1, phase: 'scan', total: few.length, records: few })).toBe(false)
    expect(worthRestoring({ savedAt: 1, phase: 'scan', total: many.length, records: many })).toBe(true)
  })
})
