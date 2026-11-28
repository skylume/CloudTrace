import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import Funnel, { type FunnelStep } from './Funnel.vue'

const STEPS: FunnelStep[] = [
  { key: 'generated', label: '生成候选', value: 300 },
  { key: 'latency_ok', label: '延迟达标', value: 120 },
  { key: 'region_ok', label: '地区解析', value: 80 },
  { key: 'usable', label: '最终可用', value: 40, tone: 'ok' },
]

describe('漏斗分段', () => {
  it('没有监听时只渲染展示块', () => {
    const wrapper = mount(Funnel, { props: { steps: STEPS } })
    expect(wrapper.findAll('button')).toHaveLength(0)
    expect(wrapper.findAll('.step')).toHaveLength(4)
  })

  it('有监听时每段变成按钮并回传 key', async () => {
    const wrapper = mount(Funnel, { props: { steps: STEPS }, attrs: { onSelect: () => {} } })
    const buttons = wrapper.findAll('button.step')
    expect(buttons).toHaveLength(4)
    await buttons[1]?.trigger('click')
    expect(wrapper.emitted('select')?.[0]).toEqual(['latency_ok'])
  })

  it('紧凑版有监听仍可点，只压尺寸', () => {
    const wrapper = mount(Funnel, {
      props: { steps: STEPS, compact: true },
      attrs: { onSelect: () => {} },
    })
    expect(wrapper.findAll('button.step')).toHaveLength(4)
    expect(wrapper.find('.funnel').classes()).toContain('compact')
  })

  it('高亮只标当前段，网格列数跟着段数走', () => {
    const steps = STEPS.map((step, index) => ({ ...step, active: index === 2 }))
    const wrapper = mount(Funnel, { props: { steps } })
    expect(wrapper.findAll('.step')[2]?.classes()).toContain('active')
    expect(wrapper.find('.funnel').attributes('style')).toContain('repeat(4')
  })
})
