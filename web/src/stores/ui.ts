/**
 * 界面状态：主题、字号、密度、动效、高对比度，以及全局提示。
 *
 * 这些设置的**权威来源是后端配置**（`ui.*`），本地存一份只是为了首屏不闪：
 * 后端还没连上时先按上次的值渲染，连上之后以服务端下发的为准。
 */
import { CMD } from '@/api/protocol'
import { defineStore } from 'pinia'
import { computed, ref, watchEffect } from 'vue'

import { sendCommand } from '@/api/client'
import type { UIConfig } from '@/api/types'

export type Theme = 'dark' | 'light' | 'system'
export type FontScale = 'small' | 'medium' | 'large'
export type TableDensity = 'compact' | 'normal' | 'comfortable'
export type Density = 'auto' | 'simple' | 'advanced'
/** 准确时间按哪个时区显示。跨时区对照日志时 UTC 省事。 */
export type TimeFormat = 'local' | 'utc'

/** 本地记忆的键。index.html 里的首屏脚本读的是同一个键。 */
const STORAGE_KEY = 'cloudtrace.ui'

interface PersistedUI {
  theme: Theme
  fontScale: FontScale
  density: Density
  animation: boolean
  contrast: boolean
  /** 侧栏是否收起。窄屏下不用它，侧栏本来就走抽屉。 */
  navCollapsed: boolean
  /** 上次停留的页面，下次打开直接回去。 */
  activeView: string
  /** 结果表每页行数。属于本机偏好，不进服务端配置。 */
  pageSize: number
}

const DEFAULTS: PersistedUI = {
  theme: 'system',
  fontScale: 'medium',
  density: 'auto',
  animation: true,
  contrast: false,
  navCollapsed: false,
  activeView: 'scan',
  pageSize: 50,
}

/**
 * load 读回上次的界面状态。
 *
 * 第二个返回值表示「本地确实存过」——它与「存过但值恰好等于默认值」是两回事：
 * 没存过时要落到配置里的启动页，存过时（且允许记忆）才回到上次那一页。
 */
function load(): { state: PersistedUI; stored: boolean } {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return { state: { ...DEFAULTS }, stored: false }
    return { state: { ...DEFAULTS, ...(JSON.parse(raw) as Partial<PersistedUI>) }, stored: true }
  } catch {
    // 读不出来就用默认值：这份记忆只是加速首屏，坏了不该让界面起不来。
    return { state: { ...DEFAULTS }, stored: false }
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
  const { state: saved, stored } = load()

  const theme = ref<Theme>(saved.theme)
  const fontScale = ref<FontScale>(saved.fontScale)
  const density = ref<Density>(saved.density)
  const animation = ref(saved.animation)
  const contrast = ref(saved.contrast)

  /**
   * 表格行高跟随字号，不再单设一项。
   *
   * 字号与行高是一件事的两面：小字配松行距、大字配紧行距都别扭。让用户分别
   * 调两次，只会调出不搭的组合。
   */
  const tableDensity = computed<TableDensity>(() => {
    if (fontScale.value === 'small') return 'compact'
    if (fontScale.value === 'large') return 'comfortable'
    return 'normal'
  })

  /**
   * 结果页当前是「结果」还是「测速」视图。
   *
   * 放在 store 而不是页面里：顶栏与其他页面要能直接把用户带到测速视图，
   * 而它们拿不到页面的局部状态。不持久化——每次打开都从「结果」开始是合理的默认。
   */
  const resultView = ref<'result' | 'speed'>('result')
  const navCollapsed = ref(saved.navCollapsed)
  const activeView = ref(saved.activeView)

  /** 是否已经定过初始页面。 */
  const stateSettled = ref(false)
  /**
   * 准确时间的显示时区。
   *
   * 固定本地时间：跨时区对照是少数场景，让每个用户先做一次选择不值得。
   */
  const timeFormat: TimeFormat = 'local'
  /** 结果表每页行数。属于本机偏好，不进服务端配置。 */
  const pageSize = ref(saved.pageSize > 0 ? saved.pageSize : 50)

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
    // density 为 auto 时保持 auto：它表示「还没决定」，由首次使用来决定。
    density.value = ui.density
    animation.value = ui.animation
    contrast.value = ui.contrast

    settleInitialView()
  }

  /**
   * settleInitialView 定下「这次打开落在哪一页」。
   *
   * 只在第一份配置到达时做一次：这个函数挂在每次配置广播上，而广播会因为改个
   * 主题、调个参数随时到来——每次都重置，用户刚点的导航就被抹掉了。
   *
   * 本地存过就留在上次那一页，第一次打开则落到扫描页。
   */
  function settleInitialView(): void {
    if (stateSettled.value) return
    stateSettled.value = true
    if (!stored) activeView.value = DEFAULTS.activeView
  }

  /**
   * setDensity 由「用户展开过高级参数」这类行为调用，并永久记住。
   *
   * 必须写回服务端，不能只改本地：配置一到就会被服务端那份覆盖，于是「展开过
   * 一次高级参数」这个偏好刷新一次就没了——而这条规则的全部意义就是「记住并
   * 直达，不必再点一次」。
   */
  function setDensity(next: Density): void {
    density.value = next
    persist({ density: next })
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
    sendCommand(CMD.settingsUpdate, { patch: { ui: patch }, origins })
  }

  /** setTheme 切换主题并写回服务端。 */
  function setTheme(next: Theme): void {
    theme.value = next
    persist({ theme: next })
  }

  /**
   * setContrast 切换高对比度并写回服务端。
   *
   * 它和主题一样存在服务端：一半界面设置存在服务端、一半存在本地，是最难向
   * 用户解释的那种状态——「为什么换台电脑就变了」。
   */
  function setContrast(next: boolean): void {
    contrast.value = next
    persist({ contrast: next })
  }

  /** setPageSize 只改本机偏好，不写服务端。 */
  function setPageSize(next: number): void {
    const size = Math.round(next)
    if (!Number.isFinite(size) || size <= 0) return
    pageSize.value = size
  }

  // 把状态贴到 DOM 与 localStorage。集中在一处，避免各组件各贴一部分。
  watchEffect(() => {
    const root = document.documentElement
    root.dataset.theme = resolvedTheme.value
    root.dataset.fontScale = fontScale.value
    root.dataset.tableDensity = tableDensity.value
    root.dataset.animation = animation.value ? 'on' : 'off'
    root.dataset.contrast = contrast.value ? 'high' : 'normal'

    const payload: PersistedUI = {
      theme: theme.value,
      fontScale: fontScale.value,
      density: density.value,
      animation: animation.value,
      contrast: contrast.value,
      navCollapsed: navCollapsed.value,
      activeView: activeView.value,
      pageSize: pageSize.value,
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
    resultView,
    navCollapsed,
    activeView,
    timeFormat,
    pageSize,
    resolvedTheme,
    showAdvanced,
    toasts,
    pushToast,
    dismissToast,
    applyFromSettings,
    setDensity,
    setTheme,
    setContrast,
    setPageSize,
  }
})
