/**
 * 全局快捷键。
 *
 * 重点守两条：**在输入框里打字时不能误触**（否则用户不敢用快捷键），以及
 * **卸载时必须移除监听**（漏掉清理会让同一次按键被执行多次，而这种问题在
 * 开发时完全看不出来）。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { defineComponent, h, ref } from 'vue'

import { useHotkeys, type Hotkey } from './hotkeys'

/** 造一个只挂了快捷键的宿主组件。 */
function mountHotkeys(hotkeys: Hotkey[]) {
  const host = defineComponent({
    setup() {
      useHotkeys(hotkeys)
      return () => h('div', [h('input', { id: 'field' })])
    },
  })
  return mount(host, { attachTo: document.body })
}

/** 在指定元素上派发一次按键。 */
function press(key: string, options: KeyboardEventInit = {}, target: EventTarget = document.body): void {
  target.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...options }))
}

describe('useHotkeys', () => {
  it('按下匹配的组合键时触发', () => {
    const handler = vi.fn()
    const wrapper = mountHotkeys([{ key: 'k', ctrl: true, handler }])

    press('k', { ctrlKey: true })

    expect(handler).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('缺少修饰键时不触发', () => {
    const handler = vi.fn()
    const wrapper = mountHotkeys([{ key: 'k', ctrl: true, handler }])

    press('k')
    press('k', { shiftKey: true })

    expect(handler).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('在输入框里打字时不触发', () => {
    const handler = vi.fn()
    const wrapper = mountHotkeys([{ key: 'f', ctrl: true, handler }])
    const field = wrapper.get('#field').element

    press('f', { ctrlKey: true }, field)

    expect(handler).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('标记了 allowInInput 的键在输入框里也触发', () => {
    const handler = vi.fn()
    const wrapper = mountHotkeys([{ key: 'escape', allowInInput: true, handler }])
    const field = wrapper.get('#field').element

    press('escape', {}, field)

    expect(handler).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('Meta 键与 Ctrl 等价', () => {
    const handler = vi.fn()
    const wrapper = mountHotkeys([{ key: 'k', ctrl: true, handler }])

    press('k', { metaKey: true })

    expect(handler).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('大写字母不匹配——按键一律按小写比较', () => {
    const handler = vi.fn()
    const wrapper = mountHotkeys([{ key: 'k', ctrl: true, handler }])

    // 浏览器在按住 Shift 时给的是大写 K，但组合键本身不区分大小写。
    press('K', { ctrlKey: true })

    expect(handler).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('卸载后不再响应', () => {
    const handler = vi.fn()
    const wrapper = mountHotkeys([{ key: 'k', ctrl: true, handler }])
    wrapper.unmount()

    press('k', { ctrlKey: true })

    expect(handler).not.toHaveBeenCalled()
  })

  it('enabled 为假时全部不响应', () => {
    const handler = vi.fn()
    const enabled = ref(false)
    const host = defineComponent({
      setup() {
        useHotkeys([{ key: 'k', ctrl: true, handler }], { enabled })
        return () => h('div')
      },
    })
    const wrapper = mount(host, { attachTo: document.body })

    press('k', { ctrlKey: true })
    expect(handler).not.toHaveBeenCalled()

    enabled.value = true
    press('k', { ctrlKey: true })
    expect(handler).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
