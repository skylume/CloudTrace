/**
 * 结果集：合并、排序、筛选与统计。
 *
 * 这里覆盖的都是「看不出错、但结果就是不对」的地方：测速结果覆盖扫描结果
 * 时把地区抹掉、不可达节点在升序里冒充最快、1.1.1.10 排到 1.1.1.2 前面。
 */
import { describe, expect, it } from 'vitest'

import { UNREACHABLE, type IPRecord } from '@/api/types'

import { mergeRecord, recordKey, sortRecords, summarize } from './results'

function record(partial: Partial<IPRecord> & { ip: string }): IPRecord {
  return {
    port: 443,
    use_tls: true,
    latency: 50,
    latency_avg: 55,
    latency_max: 60,
    jitter: 2,
    loss: 0,
    sent: 3,
    recv: 3,
    colo: '',
    loc: '',
    region_name: '',
    speed_mbps: 0,
    score: 0,
    ...partial,
  }
}

function unreachable(ip: string): IPRecord {
  return record({ ip, latency: UNREACHABLE, latency_avg: UNREACHABLE, loss: 1, recv: 0 })
}

const ips = (list: IPRecord[]) => list.map((item) => item.ip)

describe('recordKey', () => {
  it('用地址与端口共同标识一条记录', () => {
    expect(recordKey(record({ ip: '1.1.1.1' }))).toBe('1.1.1.1:443')
    expect(recordKey(record({ ip: '1.1.1.1', port: 8443 }))).toBe('1.1.1.1:8443')
  })
})

describe('mergeRecord', () => {
  it('测速结果不能把扫描阶段拿到的地区抹掉', () => {
    const scanned = record({ ip: '1.1.1.1', colo: 'HKG', region_name: '香港', asn: 13335, as_org: 'CF' })
    const tested = record({ ip: '1.1.1.1', speed_mbps: 12.5, colo: '', region_name: '', asn: 0, as_org: '' })

    const merged = mergeRecord(scanned, tested)
    expect(merged.speed_mbps).toBe(12.5)
    expect(merged.colo).toBe('HKG')
    expect(merged.region_name).toBe('香港')
    expect(merged.asn).toBe(13335)
    expect(merged.as_org).toBe('CF')
  })

  it('新记录有的值以新记录为准', () => {
    const old = record({ ip: '1.1.1.1', colo: 'HKG', latency: 90 })
    const fresh = record({ ip: '1.1.1.1', colo: 'NRT', latency: 30 })

    const merged = mergeRecord(old, fresh)
    expect(merged.colo).toBe('NRT')
    expect(merged.latency).toBe(30)
  })

  it('明细字段缺失时保留旧值', () => {
    const old = record({ ip: '1.1.1.1', trace: { colo: 'HKG' } })
    const fresh = record({ ip: '1.1.1.1' })
    expect(mergeRecord(old, fresh).trace).toEqual({ colo: 'HKG' })
  })
})

describe('sortRecords', () => {
  it('没有该项数据的记录恒排最后，与方向无关', () => {
    const list = [unreachable('1.1.1.1'), record({ ip: '1.1.1.2', latency: 50 }), record({ ip: '1.1.1.3', latency: 10 })]

    expect(ips(sortRecords(list, 'latency', false))).toEqual(['1.1.1.3', '1.1.1.2', '1.1.1.1'])
    expect(ips(sortRecords(list, 'latency', true))).toEqual(['1.1.1.2', '1.1.1.3', '1.1.1.1'])
  })

  it('没测过速的记录不因为速度是 0 就冒充最慢', () => {
    const list = [
      record({ ip: '1.1.1.1', speed_mbps: 0 }),
      record({ ip: '1.1.1.2', speed_mbps: 3 }),
      record({ ip: '1.1.1.3', speed_mbps: 9 }),
    ]

    expect(ips(sortRecords(list, 'speed_mbps', false))).toEqual(['1.1.1.2', '1.1.1.3', '1.1.1.1'])
    expect(ips(sortRecords(list, 'speed_mbps', true))).toEqual(['1.1.1.3', '1.1.1.2', '1.1.1.1'])
  })

  it('打平时按地址数值序兜底，而不是字符串序', () => {
    const list = [
      record({ ip: '1.1.1.10', latency: 20 }),
      record({ ip: '1.1.1.2', latency: 20 }),
      record({ ip: '1.1.1.9', latency: 20 }),
    ]

    expect(ips(sortRecords(list, 'latency', false))).toEqual(['1.1.1.2', '1.1.1.9', '1.1.1.10'])
  })

  it('地区维度优先按数据中心代码排序', () => {
    const list = [
      record({ ip: '1.1.1.1', colo: 'NRT', region_name: '东京' }),
      record({ ip: '1.1.1.2', colo: 'HKG', region_name: '香港' }),
      record({ ip: '1.1.1.3', colo: 'LAX', region_name: '洛杉矶' }),
    ]

    expect(ips(sortRecords(list, 'region', false))).toEqual(['1.1.1.2', '1.1.1.3', '1.1.1.1'])
  })

  it('同一份数据排两次结果一致', () => {
    const list = [record({ ip: '1.1.1.3', latency: 20 }), record({ ip: '1.1.1.1', latency: 20 })]
    const once = ips(sortRecords(list, 'latency', false))
    const twice = ips(sortRecords(list, 'latency', false))
    expect(once).toEqual(twice)
  })

  it('不改动传入的数组', () => {
    const list = [record({ ip: '1.1.1.2', latency: 50 }), record({ ip: '1.1.1.1', latency: 10 })]
    sortRecords(list, 'latency', false)
    expect(ips(list)).toEqual(['1.1.1.2', '1.1.1.1'])
  })
})

describe('summarize', () => {
  it('不可达与未测速都不参与平均', () => {
    const list = [
      record({ ip: '1.1.1.1', latency: 10, latency_avg: 10, speed_mbps: 10 }),
      record({ ip: '1.1.1.2', latency: 30, latency_avg: 30, speed_mbps: 0 }),
      unreachable('1.1.1.3'),
    ]

    const stats = summarize(list)
    expect(stats.total).toBe(3)
    expect(stats.min_latency).toBe(10)
    expect(stats.avg_latency).toBe(20)
    expect(stats.best_speed).toBe(10)
    expect(stats.avg_speed).toBe(10)
    expect(stats.qualified).toBe(1)
  })

  it('全部不可达时最低延迟是哨兵值，平均值为 0', () => {
    const stats = summarize([unreachable('1.1.1.1')])
    expect(stats.min_latency).toBe(UNREACHABLE)
    expect(stats.avg_latency).toBe(0)
    expect(stats.best_speed).toBe(0)
  })

  it('地区与运营商分布按出现次数统计', () => {
    const stats = summarize([
      record({ ip: '1.1.1.1', colo: 'HKG', as_org: 'CF' }),
      record({ ip: '1.1.1.2', colo: 'HKG', as_org: 'CF' }),
      record({ ip: '1.1.1.3', colo: 'NRT', as_org: 'AWS' }),
    ])
    expect(stats.region_dist).toEqual({ HKG: 2, NRT: 1 })
    expect(stats.asn_dist).toEqual({ CF: 2, AWS: 1 })
  })

  it('空结果集不报错', () => {
    const stats = summarize([])
    expect(stats.total).toBe(0)
    expect(stats.min_latency).toBe(UNREACHABLE)
  })
})
