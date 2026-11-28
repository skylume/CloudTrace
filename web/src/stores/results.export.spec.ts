/**
 * 复制与导出的载荷。
 *
 * 守「看到 = 复制 = 导出」：两者都读 visible，过滤与排序参数与界面同源。
 * 后端不支持的三项不传，但必须在按钮说明里交代清楚。
 */
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, expect, it } from 'vitest'

import { UNREACHABLE, type IPRecord } from '@/api/types'
import { zh } from '@/i18n/zh'

import { useFieldsStore, useResultsStore } from './results'

function record(index: number, latency: number, speed = 0): IPRecord {
  return {
    ip: `10.0.0.${index}`,
    port: 443,
    use_tls: true,
    latency,
    latency_avg: latency,
    latency_max: latency,
    jitter: 1,
    loss: 0,
    sent: 4,
    recv: 4,
    colo: 'HKG',
    loc: 'HK',
    region_name: '香港',
    speed_mbps: speed,
    score: 0,
  }
}

const unreachable = (index: number): IPRecord => ({ ...record(index, UNREACHABLE), recv: 0 })

beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
})

it('复制条数与顺序与可见行一致', () => {
  const store = useResultsStore()
  store.replaceAll([record(3, 30), record(1, 10), record(2, 20)])
  store.sortKey = 'latency'
  store.sortDesc = false

  const lines = store.copyPayload().split('\n')
  expect(lines).toHaveLength(store.visibleRecords.length)
  expect(lines).toEqual(store.visibleRecords.map((item) => `${item.ip}:${item.port}`))
  expect(store.copyPayload(2)).toBe(lines.slice(0, 2).join('\n'))
})

it('导出载荷的过滤与排序跟随视图', () => {
  const store = useResultsStore()
  const fields = useFieldsStore()
  store.replaceAll([record(1, 10), record(2, 20)])
  store.maxLatency = 15
  store.sortKey = 'latency'
  store.sortDesc = false

  const payload = store.exportPayload()
  expect(payload.fields).toEqual(fields.visibleKeys)
  expect(payload.filter).toEqual({ max_latency: 15, include_unreached: true })
  expect(payload.sort).toBe('latency')
  expect(payload.desc).toBe(false)
})

it('没有延迟上限时导出传 0，表示不限制', () => {
  const store = useResultsStore()
  store.replaceAll([record(1, 10)])
  store.maxLatency = null
  expect(store.exportPayload().filter.max_latency).toBe(0)
})

it('质量排序不进导出参数：后端不认这个键', () => {
  const store = useResultsStore()
  store.replaceAll([record(1, 10, 8), unreachable(2)])
  store.sortKey = 'quality'
  store.sortDesc = true

  const payload = store.exportPayload()
  expect(payload.sort).toBe('')
  expect('desc' in payload).toBe(false)
  // 界面顺序仍是质量降序：合格在前，不可达在后。
  expect(store.visibleRecords.map((item) => item.recv > 0)).toEqual([true, false])
})

it('按钮说明交代了只影响界面的四项', () => {
  const hint = zh['result.exportHint']
  expect(hint).toContain('地区筛选')
  expect(hint).toContain('丢包上限')
  expect(hint).toContain('仅显示达标')
  expect(hint).toContain('搜索')
})
