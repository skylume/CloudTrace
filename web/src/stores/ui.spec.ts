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
      remember_state: true,
      adaptive_enabled: true,
      adaptive_allow_preset: true,
    })

    expect(sendCommand).not.toHaveBeenCalled()
  })
})
