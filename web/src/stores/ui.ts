/**
 * 界面状态：主题、字号、密度、动效、高对比度，以及全局提示。
 *
 * 这些设置的**权威来源是后端配置**（`ui.*`），本地存一份只是为了首屏不闪：
 * 后端还没连上时先按上次的值渲染，连上之后以服务端下发的为准。
 */
import { defineStore } from 'pinia'
import { computed, ref, watchEffect } from 'vue'

import { sendCommand } from '@/api/client'
import type { UIConfig } from '@/api/types'
import { setLocale, type Locale } from '@/i18n'

export type Theme = 'dark' | 'light' | 'system'
export type FontScale = 'small' | 'medium' | 'large'
export type TableDensity = 'compact' | 'normal' | 'comfortable'
export type Density = 'auto' | 'simple' | 'advanced'

/** 本地记忆的键。index.html 里的首屏脚本读的是同一个键。 */
const STORAGE_KEY = 'cloudtrace.ui'

interface PersistedUI {
  theme: Theme
  fontScale: FontScale
  tableDensity: TableDensity
  density: Density
  animation: boolean
  contrast: boolean
  lang: Locale
  /** 侧栏是否收起。窄屏下不用它，侧栏本来就走抽屉。 */
  navCollapsed: boolean
  /** 上次停留的页面，下次打开直接回去。 */
  activeView: string
}

const DEFAULTS: PersistedUI = {
  theme: 'system',
  fontScale: 'medium',
  tableDensity: 'normal',
  density: 'auto',
  animation: true,
  contrast: false,
  lang: 'zh',
  navCollapsed: false,
  activeView: 'scan',
}

function load(): PersistedUI {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return { ...DEFAULTS }
    return { ...DEFAULTS, ...(JSON.parse(raw) as Partial<PersistedUI>) }
  } catch {
    // 读不出来就用默认值：这份记忆只是加速首屏，坏了不该让界面起不来。
    return { ...DEFAULTS }
  }
}

export interface Toast {
  id: number
  kind: 'info' | 'ok' | 'warn' | 'bad'
  message: string
  /** 可撤销操作给出的动作。 */
  action?: { label: string; run: () => void }
  /** 毫秒；0 表示不自动消失。 */
  timeout: number
}

let toastSeq = 0

export const useUIStore = defineStore('ui', () => {
  const saved = load()

  const theme = ref<Theme>(saved.theme)
  const fontScale = ref<FontScale>(saved.fontScale)
  const tableDensity = ref<TableDensity>(saved.tableDensity)
  const density = ref<Density>(saved.density)
  const animation = ref(saved.animation)
  const contrast = ref(saved.contrast)
  const lang = ref<Locale>(saved.lang)
  const navCollapsed = ref(saved.navCollapsed)
  const activeView = ref(saved.activeView)

  /** 系统当前是不是深色。跟随系统时用它决定实际主题。 */
  const systemDark = ref(true)

  const media = typeof matchMedia === 'function' ? matchMedia('(prefers-color-scheme: dark)') : null
  if (media) {
    systemDark.value = media.matches
    media.addEventListener('change', (event) => {
      systemDark.value = event.matches
    })
  }

  /**
   * resolvedTheme 是真正贴到 DOM 上的值，只会是 dark 或 light。
   *
   * 把「跟随系统」在 JS 里解析掉，样式表里就不需要写 media query，
   * 浅色配色也就不必写两遍——两遍必然会漏同步。
   */
  const resolvedTheme = computed<Exclude<Theme, 'system'>>(() => {
    if (theme.value === 'system') return systemDark.value ? 'dark' : 'light'
    return theme.value
  })

  const toasts = ref<Toast[]>([])

  function pushToast(input: Omit<Toast, 'id' | 'timeout'> & { timeout?: number }): number {
    const id = ++toastSeq
    const toast: Toast = { id, timeout: 4000, ...input }
    toasts.value = [...toasts.value, toast]
    if (toast.timeout > 0) {
      setTimeout(() => dismissToast(id), toast.timeout)
    }
    return id
  }

  function dismissToast(id: number): void {
    toasts.value = toasts.value.filter((item) => item.id !== id)
  }

  /** applyFromSettings 把服务端下发的界面配置吸收进来。 */
  function applyFromSettings(ui: UIConfig): void {
    theme.value = ui.theme
    fontScale.value = ui.font_scale
    tableDensity.value = ui.table_density
    // density 为 auto 时保持 auto：它表示「还没决定」，由首次使用来决定。
    density.value = ui.density
    animation.value = ui.animation
    lang.value = ui.lang
  }

  /** setDensity 由「用户展开过高级参数」这类行为调用，并永久记住。 */
  function setDensity(next: Density): void {
    density.value = next
  }

  /**
   * 把界面层的改动写回服务端。
   *
   * 必须写回，不能只改本地：服务端那份配置是唯一权威，而设置页每次进入都会
   * 重新拉全量配置。只改本地的话，用户从顶栏切到深色、再点开设置页，就会被
   * 服务端那份「跟随系统」覆盖回去——表现就是「切了没用」。
   *
   * 写回之后服务端会广播 settings，本地再以广播为准，两边始终一致。
   */
  function persist(patch: Record<string, unknown>): void {
    const origins: Record<string, 'user'> = {}
    for (const key of Object.keys(patch)) origins[`ui.${key}`] = 'user'
    sendCommand('settings/update', { patch: { ui: patch }, origins })
  }

  /** setTheme 切换主题并写回服务端。 */
  function setTheme(next: Theme): void {
    theme.value = next
    persist({ theme: next })
  }

  /** setLang 切换语言并写回服务端。 */
  function setLang(next: Locale): void {
    lang.value = next
    persist({ lang: next })
  }

  // 把状态贴到 DOM 与 localStorage。集中在一处，避免各组件各贴一部分。
  watchEffect(() => {
    const root = document.documentElement
    root.dataset.theme = resolvedTheme.value
    root.dataset.fontScale = fontScale.value
    root.dataset.tableDensity = tableDensity.value
    root.dataset.animation = animation.value ? 'on' : 'off'
    root.dataset.contrast = contrast.value ? 'high' : 'normal'
    setLocale(lang.value)

    const payload: PersistedUI = {
      theme: theme.value,
      fontScale: fontScale.value,
      tableDensity: tableDensity.value,
      density: density.value,
      animation: animation.value,
      contrast: contrast.value,
      lang: lang.value,
      navCollapsed: navCollapsed.value,
      activeView: activeView.value,
    }
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(payload))
    } catch {
      /* 隐私模式下写不进去，忽略即可 */
    }
  })

  /** 简单 / 高级的最终取值：auto 表示还没决定，按「简单」处理。 */
  const showAdvanced = computed(() => density.value === 'advanced')

  return {
    theme,
    fontScale,
    tableDensity,
    density,
    animation,
    contrast,
    lang,
    navCollapsed,
    activeView,
    resolvedTheme,
    showAdvanced,
    toasts,
    pushToast,
    dismissToast,
    applyFromSettings,
    setDensity,
    setTheme,
    setLang,
  }
})
