import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'

import { t } from '@/i18n'
import { useResultsStore } from '@/stores/results'
import ResultToolbar from './ResultToolbar.vue'

beforeEach(() => {
  setActivePinia(createPinia())
})

async function mountToolbar() {
  const wrapper = mount(ResultToolbar, { props: { view: 'result' } })
  const more = wrapper.findAll('button').find((button) => button.text() === t('common.more'))
  if (!more) throw new Error('找不到「更多」按钮')
  await more.trigger('click')
  return wrapper
}

describe('结果页工具栏', () => {
  it('低频动作默认收在「更多」里', () => {
    const wrapper = mount(ResultToolbar, { props: { view: 'result' } })
    expect(wrapper.find('#ct-result-more').exists()).toBe(false)
  })

  it('打开「更多」后才出现筛选与复制入口', async () => {
    const wrapper = await mountToolbar()
    expect(wrapper.find('#ct-result-more').exists()).toBe(true)
    expect(wrapper.text()).toContain(t('result.copyAll'))
  })

  it('丢包芯片点一次生效，再点一次取消', async () => {
    const wrapper = await mountToolbar()
    const results = useResultsStore()
    const chip = wrapper.findAll('button.chip').find((button) => button.text() === '5%')
    if (!chip) throw new Error('找不到 5% 丢包芯片')

    await chip.trigger('click')
    expect(results.maxLoss).toBe(0.05)
    await chip.trigger('click')
    expect(results.maxLoss).toBe(null)
  })
})
