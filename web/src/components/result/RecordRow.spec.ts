import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'

import type { FieldDef, IPRecord } from '@/api/types'

import GroupRow from './GroupRow.vue'
import RecordRow from './RecordRow.vue'

const BRIEF = ['ip', 'region_name', 'latency', 'loss', 'speed_mbps', 'score']
const STANDARD = [
  'ip',
  'region_name',
  'colo',
  'latency',
  'latency_avg',
  'jitter',
  'loss',
  'use_tls',
  'speed_mbps',
  'score',
]
const FULL = [
  'ip',
  'region_name',
  'colo',
  'latency',
  'latency_avg',
  'latency_max',
  'jitter',
  'loss',
  'use_tls',
  'speed_mbps',
  'score',
  'asn',
  'as_org',
  'geo_warn',
]

const TYPES: Record<string, FieldDef['type']> = {
  ip: 'string',
  region_name: 'string',
  colo: 'string',
  latency: 'float',
  latency_avg: 'float',
  latency_max: 'float',
  jitter: 'float',
  loss: 'float',
  use_tls: 'bool',
  speed_mbps: 'float',
  score: 'float',
  asn: 'int',
  as_org: 'string',
  geo_warn: 'string',
}

function columnsOf(keys: string[]): FieldDef[] {
  return keys.map((key) => ({
    key,
    label: key,
    type: TYPES[key] ?? 'string',
    group: 'basic',
    default: true,
  }))
}

function record(): IPRecord {
  return {
    ip: '10.0.0.1',
    port: 443,
    use_tls: true,
    latency: 42,
    latency_avg: 45,
    latency_max: 60,
    jitter: 2,
    loss: 0,
    sent: 4,
    recv: 4,
    colo: 'HKG',
    loc: 'HK',
    region_name: '香港',
    speed_mbps: 0,
    score: 0,
  }
}

beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
})

describe('结果行固定列', () => {
  it.each([
    ['结果视图', false],
    ['测速视图', true],
  ])('%s下四个固定列常驻，数据列数不改变它们', (_name, speedView) => {
    const columns = columnsOf(STANDARD)
    const wrapper = mount(RecordRow, {
      props: { record: record(), columns, rank: 1, expanded: true, speedView },
    })

    const cells = wrapper.find('tr.expandable').findAll('td')
    expect(cells).toHaveLength(columns.length + 4)
    expect(cells[0]?.classes()).toContain('ct-sticky-1')
    expect(cells[1]?.classes()).toContain('ct-sticky-2')
    expect(cells[2]?.classes()).toContain('ct-sticky-3')
    expect(cells[3]?.classes()).toContain('ct-sticky-4')
    expect(cells[4]?.classes()).toContain('ct-sticky-5')

    const detail = wrapper.find('tr.detail-row')
    expect(detail.find('td').attributes('colspan')).toBe(String(columns.length + 4))
  })
})

describe('分组父行跨列', () => {
  it.each([
    ['简版', BRIEF],
    ['标准', STANDARD],
    ['完整', FULL],
  ])('%s预设下合计等于数据列数 + 4', (_name, keys) => {
    const columnCount = keys.length
    const wrapper = mount(GroupRow, {
      props: {
        group: {
          key: 'HKG',
          label: '香港',
          records: [],
          count: 3,
          minLatency: 20,
          avgLatency: 30,
          bestSpeed: 5,
        },
        expanded: false,
        selectable: true,
        columnCount,
      },
    })

    const cells = wrapper.findAll('tr.group > td')
    expect(cells).toHaveLength(4)
    const total = cells.reduce((sum, cell) => sum + Number(cell.attributes('colspan') ?? 1), 0)
    expect(total).toBe(columnCount + 4)
    // 摘要吃掉除右按钮之外的全部数据列。
    expect(cells[2]?.attributes('colspan')).toBe(String(columnCount - 1))
  })
})
