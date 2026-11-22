import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { IPRecord } from '@/api/types'

import NearestNotice from './NearestNotice.vue'

function record(ip: string, latency: number): IPRecord {
  return { ip, port: 443, use_tls: true, latency, loss: 0, sent: 4, recv: 4 } as IPRecord
}

function mountNotice(items: IPRecord[]) {
  return mount(NearestNotice, { props: { threshold: 100, items } })
}

describe('零结果回看', () => {
  it('建议阈值按最短延迟留一成余量', () => {
    const wrapper = mountNotice([record('1.1.1.1', 120), record('1.1.1.2', 180)])
    // 120 × 1.1 = 132，向上取整仍是 132。
    expect(wrapper.text()).toContain('132')
    expect(wrapper.text()).toContain('120')
  })

  it('点重跑发出建议阈值，点返回发出 back', async () => {
    const wrapper = mountNotice([record('1.1.1.1', 120)])
    const buttons = wrapper.findAll('button')
    await buttons[0]?.trigger('click')
    await buttons[1]?.trigger('click')
    expect(wrapper.emitted('retry')?.[0]).toEqual([132])
    expect(wrapper.emitted('back')).toHaveLength(1)
  })

  it('延迟值全是哨兵时不显示重跑按钮', () => {
    const wrapper = mountNotice([record('1.1.1.1', -1)])
    const labels = wrapper.findAll('button').map((button) => button.text())
    expect(labels).toHaveLength(1)
  })
})
