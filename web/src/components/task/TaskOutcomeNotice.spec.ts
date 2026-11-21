/**
 * 任务结局的展示。
 *
 * 这里钉的是「后端字段到界面」这一跳：字段名（stage / duration_ms /
 * warnings）与文案拼装一旦对不上，界面不会报错，只会少一块内容。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { TaskOutcome } from '@/stores/task'

import TaskOutcomeNotice from './TaskOutcomeNotice.vue'

function outcome(patch: Partial<TaskOutcome> = {}): TaskOutcome {
  return { status: 'done', stage: 'scan', durationMs: 1500, warnings: [], ...patch }
}

describe('TaskOutcomeNotice', () => {
  it('失败时同时给出阶段与耗时', () => {
    const wrapper = mount(TaskOutcomeNotice, {
      props: { outcome: outcome({ status: 'failed' }) },
    })

    const text = wrapper.text()
    expect(text).toContain('扫描没能跑完')
    expect(text).toContain('卡在「扫描」')
    expect(text).toContain('1.5 秒')
  })

  it('没有阶段与耗时时不编造数字', () => {
    const wrapper = mount(TaskOutcomeNotice, {
      props: { outcome: outcome({ status: 'failed', stage: '', durationMs: 0 }) },
    })

    expect(wrapper.text()).toContain('任务没能跑完')
    expect(wrapper.text()).not.toContain('耗时')
  })

  it('列出告警，并只说明一次「不影响结果」', () => {
    const wrapper = mount(TaskOutcomeNotice, {
      props: {
        outcome: outcome({ warnings: ['ASN 库不可用，结果里没有运营商', '部分节点未解析出地区'] }),
      },
    })

    expect(wrapper.findAll('.warn-list li')).toHaveLength(2)
    expect(wrapper.text()).toContain('2 条提醒')
    expect(wrapper.text()).toContain('不影响这次结果')
  })

  it('没有告警时不留空容器', () => {
    const wrapper = mount(TaskOutcomeNotice, { props: { outcome: outcome() } })

    expect(wrapper.find('details').exists()).toBe(false)
    expect(wrapper.find('.ct-card').exists()).toBe(false)
  })
})
