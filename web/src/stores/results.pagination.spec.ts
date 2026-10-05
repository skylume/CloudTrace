/**
 * 结果集的分页。
 *
 * 这里守的是「页码与列表必须同源」：筛选一变，列表立刻变短，而页码可能还停在
 * 很后面——夹不住就会出现「第 7 / 2 页」这种自相矛盾的状态，或者一张空表。
 */
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, expect, it } from 'vitest'

import type { IPRecord } from '@/api/types'

import { useResultsStore } from './results'

const PAGE_SIZE = 50

function record(index: number): IPRecord {
  return {
    ip: `10.0.0.${index}`,
    port: 443,
    use_tls: true,
    latency: index,
    latency_avg: index,
    latency_max: index,
    jitter: 2,
    loss: 0,
    sent: 3,
    recv: 3,
    colo: '',
    loc: '',
    region_name: '',
    speed_mbps: 0,
    score: 0,
  }
}

function seed(count: number) {
  const store = useResultsStore()
  const list: IPRecord[] = []
  for (let i = 1; i <= count; i += 1) list.push(record(i))
  store.replaceAll(list)
  return store
}

const ips = (list: IPRecord[]) => list.map((item) => item.ip)

beforeEach(() => setActivePinia(createPinia()))

it('没有结果时也算一页', () => {
  const store = useResultsStore()
  expect(store.pageCount).toBe(1)
  expect(store.paged).toHaveLength(0)
})

it('按每页条数切分', () => {
  const store = seed(120)

  expect(store.paged).toHaveLength(PAGE_SIZE)
  store.setPage(3)
  // 120 条、每页 50：第三页只剩 20 条。
  expect(store.paged).toHaveLength(120 - 2 * PAGE_SIZE)
})

it('页码越界时夹回合法范围', () => {
  const store = seed(60)

  store.setPage(999)
  expect(store.currentPage).toBe(store.pageCount)

  store.setPage(0)
  expect(store.currentPage).toBe(1)

  store.setPage(-5)
  expect(store.currentPage).toBe(1)
})

it('第二页从第 51 条开始', () => {
  const store = seed(120)
  store.setPage(2)

  expect(store.currentPage).toBe(2)
  expect(ips(store.paged)[0]).toBe(ips(store.visible)[PAGE_SIZE])
})

/**
 * 筛选一变就回第一页。
 *
 * 不回的话，用户停在第 3 页改一下筛选，看到的就是一张空表——而结果其实有，
 * 只是都在前两页。
 */
it('改筛选条件回到第一页', () => {
  const store = seed(120)
  store.setPage(3)
  expect(store.currentPage).toBe(3)

  store.keyword = '10.0.0.1'
  expect(store.currentPage).toBe(1)
})

/**
 * 表头的复选框只选本页。
 *
 * 分页之后「全选」如果指全部筛选结果，用户会选中一堆他看不见的行——接着点
 * 「导出」，拿到的东西和他以为选中的完全不是一回事。
 */
it('表头全选只选本页', () => {
  const store = seed(120)
  store.selectAllOnPage()

  expect(store.selected.size).toBe(store.paged.length)
  expect(store.selected.size).toBeLessThan(store.visible.length)
})

it('翻页之后全选选的是新的那一页', () => {
  const store = seed(120)
  store.setPage(3)
  store.selectAllOnPage()

  const third = ips(store.paged)
  expect(third.every((ip) => store.selected.has(`${ip}:443`))).toBe(true)
  expect(store.selected.has(`${ips(store.visible)[0]}:443`)).toBe(false)
})
