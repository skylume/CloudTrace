/**
 * 结果视图的筛选与记忆。
 *
 * 守两件事：丢包上限只筛「有响应的记录」，以及视图记忆必须可读回、可还原。
 * 记忆没有出口就是陷阱——用户调乱之后只能清浏览器数据才能回去。
 */
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import { beforeEach, expect, it } from 'vitest'

import { UNREACHABLE, type IPRecord } from '@/api/types'

import { useResultsStore } from './results'

const VIEW_KEY = 'cloudtrace.resultView'

function record(index: number, loss: number): IPRecord {
  return {
    ip: `10.0.0.${index}`,
    port: 443,
    use_tls: true,
    latency: index,
    latency_avg: index,
    latency_max: index,
    jitter: 2,
    loss,
    sent: 4,
    recv: 4,
    colo: '',
    loc: '',
    region_name: '',
    speed_mbps: 0,
    score: 0,
  }
}

/** 不可达的记录：没有响应，延迟是哨兵值。 */
function unreachable(index: number): IPRecord {
  return { ...record(index, 1), latency: UNREACHABLE, recv: 0 }
}

const ips = (list: IPRecord[]) => list.map((item) => item.ip)

beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
})

it('丢包上限滤掉超阈值的记录', () => {
  const store = useResultsStore()
  store.replaceAll([record(1, 0), record(2, 0.02), record(3, 0.2)])

  store.maxLoss = 0.01
  expect(ips(store.visible)).toEqual(['10.0.0.1'])

  store.maxLoss = 0.05
  expect(ips(store.visible)).toEqual(['10.0.0.1', '10.0.0.2'])
})

it('不可达的记录不按丢包筛掉', () => {
  const store = useResultsStore()
  store.replaceAll([record(1, 0), unreachable(2)])

  store.maxLoss = 0
  // 不可达是「没响应」，不是「丢包超标」，由延迟与达标筛选负责。
  expect(ips(store.visible)).toEqual(['10.0.0.1', '10.0.0.2'])
})

it('视图变更写进本地记忆', async () => {
  const store = useResultsStore()
  store.regionFilter = ['HKG']
  store.maxLoss = 0.05
  store.sortKey = 'score'
  store.sortDesc = true
  await nextTick()

  const saved = JSON.parse(localStorage.getItem(VIEW_KEY) ?? '{}') as Record<string, unknown>
  expect(saved.regionFilter).toEqual(['HKG'])
  expect(saved.maxLoss).toBe(0.05)
  expect(saved.sortKey).toBe('score')
  expect(saved.sortDesc).toBe(true)
})

it('重新打开时读回上次视图', async () => {
  const store = useResultsStore()
  store.regionFilter = ['HKG']
  store.maxLoss = 0.05
  store.groupBy = 'colo'
  await nextTick()

  setActivePinia(createPinia())
  const reopened = useResultsStore()
  expect(reopened.regionFilter).toEqual(['HKG'])
  expect(reopened.maxLoss).toBe(0.05)
  expect(reopened.groupBy).toBe('colo')
})

it('还原默认视图清掉筛选并回到默认排序', async () => {
  const store = useResultsStore()
  store.replaceAll([record(1, 0), record(2, 0.2)])
  store.regionFilter = ['HKG']
  store.maxLatency = 200
  store.maxLoss = 0.01
  store.qualifiedOnly = true
  store.sortKey = 'score'
  store.sortDesc = true
  store.groupBy = 'asn'
  store.keyword = '10.0'
  await nextTick()

  store.resetView()
  await nextTick()

  expect(store.regionFilter).toEqual([])
  expect(store.maxLatency).toBeNull()
  expect(store.maxLoss).toBeNull()
  expect(store.qualifiedOnly).toBe(false)
  expect(store.sortKey).toBe('loss')
  expect(store.sortDesc).toBe(false)
  expect(store.groupBy).toBe('none')
  expect(store.keyword).toBe('')
  expect(store.visible).toHaveLength(2)
})

it('记忆坏掉时回默认，而不是整张表消失', () => {
  localStorage.setItem(VIEW_KEY, '{not json')
  const store = useResultsStore()

  expect(store.regionFilter).toEqual([])
  expect(store.maxLoss).toBeNull()
  expect(store.sortKey).toBe('loss')
})
