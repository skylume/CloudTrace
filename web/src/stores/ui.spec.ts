/**
 * 界面状态的持久化。
 *
 * 这里守的是一条容易被忽略的约束：**界面层的改动必须写回服务端**。
 *
 * 曾经的表现是「从顶栏切到深色，点开设置页又变回浅色」——因为顶栏只改了本地
 * 状态，而设置页每次进入都会重新拉全量配置，服务端那份还是旧值，广播回来就
 * 把本地覆盖了。用户看到的是「切了没用」，根因是同一份设置存了两处。
 */
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

// 声明成接收任意参数：下面的 mock 工厂要把它透传出去，
// 零参数的 vi.fn 会让展开实参在类型上过不去。
const sendCommand = vi.fn((..._args: unknown[]) => true)

vi.mock('@/api/client', () => ({
  sendCommand: (...args: unknown[]) => sendCommand(...args),
}))

import type { UIConfig } from '@/api/types'

const { useUIStore } = await import('./ui')

beforeEach(() => {
  setActivePinia(createPinia())
  sendCommand.mockClear()
  localStorage.clear()
})

describe('主题与语言', () => {
  it('切主题时同时写回服务端', () => {
    const ui = useUIStore()

    ui.setTheme('dark')

    expect(ui.theme).toBe('dark')
    expect(sendCommand).toHaveBeenCalledWith('settings/update', {
      patch: { ui: { theme: 'dark' } },
      origins: { 'ui.theme': 'user' },
    })
  })

  it('切语言时同时写回服务端', () => {
    const ui = useUIStore()

    ui.setLang('en')

    expect(ui.lang).toBe('en')
    expect(sendCommand).toHaveBeenCalledWith('settings/update', {
      patch: { ui: { lang: 'en' } },
      origins: { 'ui.lang': 'user' },
    })
  })

  it('跟随系统在本地解析成具体主题，DOM 上只会有 dark 或 light', () => {
    const ui = useUIStore()

    ui.setTheme('system')

    expect(['dark', 'light']).toContain(ui.resolvedTheme)
  })
})

describe('披露程度', () => {
  /**
   * 展开过一次高级参数之后要「永久记住并直达」。
   *
   * 只改本地是记不住的：配置一到就被服务端那份覆盖回去，刷新一次又变回简单。
   */
  it('切密度要写回服务端', () => {
    const ui = useUIStore()

    ui.setDensity('advanced')

    expect(ui.density).toBe('advanced')
    expect(sendCommand).toHaveBeenCalledWith('settings/update', {
      patch: { ui: { density: 'advanced' } },
      origins: { 'ui.density': 'user' },
    })
  })

  it('展开与收起各记一次', () => {
    const ui = useUIStore()

    ui.setDensity('advanced')
    ui.setDensity('simple')

    expect(sendCommand).toHaveBeenLastCalledWith('settings/update', {
      patch: { ui: { density: 'simple' } },
      origins: { 'ui.density': 'user' },
    })
  })
})

describe('记住界面状态与启动页', () => {
  /** 一份完整的界面配置，只覆盖关心的那两项。 */
  function config(overrides: Partial<UIConfig>): UIConfig {
    return {
      theme: 'dark',
      lang: 'zh',
      font_scale: 'medium',
      density: 'advanced',
      table_density: 'normal',
      page_size: 50,
      time_format: 'local',
      start_page: 'scan',
      animation: true,
      contrast: false,
      remember_state: true,
      adaptive_enabled: true,
      adaptive_allow_preset: true,
      ...overrides,
    }
  }

  /**
   * 关掉记忆时每次从启动页面开始。
   *
   * 这个开关以前没有任何代码读它——改它什么都不会发生，而界面上它看起来是个
   * 正常的开关。
   */
  it('关掉记忆时从启动页面开始，并回到默认密度', () => {
    const ui = useUIStore()

    ui.applyFromSettings(config({ remember_state: false, start_page: 'history' }))

    expect(ui.activeView).toBe('history')
    expect(ui.density).toBe('auto')
  })

  it('没有存过状态时落到启动页面', () => {
    const ui = useUIStore()

    ui.applyFromSettings(config({ remember_state: true, start_page: 'result' }))

    expect(ui.activeView).toBe('result')
  })

  it('允许记忆且存过时留在上次那一页', () => {
    localStorage.setItem('cloudtrace.ui', JSON.stringify({ activeView: 'settings' }))
    const ui = useUIStore()

    ui.applyFromSettings(config({ remember_state: true, start_page: 'scan' }))

    expect(ui.activeView).toBe('settings')
  })

  /**
   * 只定一次。
   *
   * 这个判定挂在每次配置广播上，而广播会因为改个主题、调个参数随时到来——
   * 每次都重置的话，用户刚点的导航就被抹掉了。
   */
  it('之后的配置广播不把用户刚做的导航抹掉', () => {
    const ui = useUIStore()
    ui.applyFromSettings(config({ remember_state: false, start_page: 'scan' }))

    ui.activeView = 'history'
    ui.applyFromSettings(config({ remember_state: false, start_page: 'scan' }))

    expect(ui.activeView).toBe('history')
  })
})

describe('服务端下发的界面配置', () => {
  it('全量替换本地状态', () => {
    const ui = useUIStore()

    ui.applyFromSettings({
      theme: 'light',
      lang: 'en',
      font_scale: 'large',
      density: 'advanced',
      table_density: 'compact',
      page_size: 50,
      time_format: 'local',
      start_page: 'result',
      animation: false,
      contrast: false,
      remember_state: true,
      adaptive_enabled: true,
      adaptive_allow_preset: false,
    })

    expect(ui.theme).toBe('light')
    expect(ui.lang).toBe('en')
    expect(ui.fontScale).toBe('large')
    expect(ui.tableDensity).toBe('compact')
    expect(ui.animation).toBe(false)
  })

  it('吸收服务端配置本身不触发写回，否则会形成回环', () => {
    const ui = useUIStore()

    ui.applyFromSettings({
      theme: 'dark',
      lang: 'zh',
      font_scale: 'medium',
      density: 'auto',
      table_density: 'normal',
      page_size: 50,
      time_format: 'local',
      start_page: 'scan',
      animation: true,
      contrast: false,
      remember_state: true,
      adaptive_enabled: true,
      adaptive_allow_preset: true,
    })

    expect(sendCommand).not.toHaveBeenCalled()
  })
})
