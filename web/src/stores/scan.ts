/**
 * 扫描页的一组状态：可触发的动作、自适应调整、运行日志、重跑交接。
 *
 * 它们都只服务扫描这一条线，生命周期也一致：页面挂载时注册动作、开始跑时
 * 记日志、跑完把参数交给下一次重跑。拆成四个 store 只会让「扫描现在是什么
 * 状态」散落在四处，合并之后一处就能看全。
 */
import { CMD } from '@/api/protocol'
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { sendCommand } from '@/api/client'
import { t } from '@/i18n'
import { buildPatch } from '@/i18n/settingsSchema'

/** 一个可被快捷键触发的动作。 */
export type ActionHandler = () => void

export interface AdaptiveNotice {
  key: string
  from: number
  to: number
  /** 机器可读的原因标识，文案由界面层决定。 */
  reason: string
}

export interface LogLine {
  /** 毫秒时间戳，渲染时格式化成时分秒。 */
  at: number
  level: 'info' | 'warn' | 'bad'
  text: string
}

/** 一次重跑请求：面板参数（驼峰）+ 来源。 */
export interface RerunRequest {
  /** 面板参数，键与 i18n 映射表一致（如 `workers`）。 */
  params: Record<string, number | boolean>
  /** 自定义文本来源。官方网段与远端地址取自配置，不随记录走。 */
  customText: string
}

/** 保留的行数上限。日志是给人扫一眼的，不是归档。 */
const MAX_LINES = 200

export const useScanStore = defineStore('scan', () => {
  // ---- 动作注册 ----
  const startScan = ref<ActionHandler | null>(null)
  const startSpeed = ref<ActionHandler | null>(null)

  /** 页面挂载时注册；返回注销函数，页面卸载时调用。 */
  function register(name: 'startScan' | 'startSpeed', handler: ActionHandler): () => void {
    if (name === 'startScan') startScan.value = handler
    else startSpeed.value = handler
    return () => {
      if (name === 'startScan') startScan.value = null
      else startSpeed.value = null
    }
  }

  function runStartScan(): boolean {
    if (!startScan.value) return false
    startScan.value()
    return true
  }

  function runStartSpeed(): boolean {
    if (!startSpeed.value) return false
    startSpeed.value()
    return true
  }

  // ---- 自适应 ----
  /** 被自动调整过的参数：key → 调整详情。 */
  const applied = ref<Record<string, AdaptiveNotice>>({})
  /** 只给建议、值未变的参数。 */
  const suggestions = ref<AdaptiveNotice[]>([])
  /** 用户点过「不再提示」的建议原因。 */
  const muted = ref<string[]>([])
  /** 正在探测网络环境（智能推荐要先探一次出口运营商）。 */
  const recommending = ref(false)

  const visibleSuggestions = computed(() =>
    suggestions.value.filter((item) => !muted.value.includes(item.reason)),
  )

  function applyApplied(notice: AdaptiveNotice): void {
    applied.value = { ...applied.value, [notice.key]: notice }
  }

  function applySuggestion(notice: AdaptiveNotice): void {
    // 同一个参数同一条建议只留一条，避免每次选源都堆一条。
    const others = suggestions.value.filter((item) => item.key !== notice.key)
    suggestions.value = [...others, notice]
  }

  /**
   * 还原一个被自动调整的参数。
   *
   * 还原的是「调整前那个值」，不是配置默认值——用户要的是回到刚才，而不是
   * 回到出厂设置。
   */
  function revert(key: string): void {
    const notice = applied.value[key]
    if (!notice) return
    // 还原之后这个值就是用户的了，标记成 user，自适应不会再动它。
    sendCommand(CMD.settingsUpdate, {
      patch: buildPatch(key, notice.from),
      origins: { [key]: 'user' },
    })

    const next = { ...applied.value }
    delete next[key]
    applied.value = next
  }

  /** 应用一条建议：等同于用户自己把值改成建议值。 */
  function accept(notice: AdaptiveNotice): void {
    // 用户点了「一键应用」，这个值从此就是他的选择，自适应不再碰。
    sendCommand(CMD.settingsUpdate, {
      patch: buildPatch(notice.key, notice.to),
      origins: { [notice.key]: 'user' },
    })
    dismiss(notice.key)
  }

  /** 忽略一条建议：值不变，只是不再显示这一条。 */
  function dismiss(key: string): void {
    suggestions.value = suggestions.value.filter((item) => item.key !== key)
  }

  /** 不再提示这类建议。 */
  function mute(reason: string): void {
    if (!muted.value.includes(reason)) muted.value = [...muted.value, reason]
  }

  /** 原因标识 → 人话。认不出来就显示原标识，至少不是空白。 */
  function reasonText(reason: string): string {
    const key = `adaptive.${reason}`
    const translated = t(key as never)
    return translated === key ? reason : translated
  }

  /**
   * 智能推荐：按当前网络环境给一组调整。
   *
   * 与自动自适应的区别是它不受来源限制——用户点了按钮就是明确授权，包括改他
   * 手填过的值。改动照样走 adaptive/applied 事件回来，因此徽标与「还原」都还在。
   *
   * 服务端要先探一次出口运营商（有十分钟缓存），所以给一个进行中的状态：不
   * 给的话按钮点下去几秒钟没反应，用户会以为它坏了。
   */
  function recommend(): void {
    if (recommending.value) return
    recommending.value = true
    if (!sendCommand('adaptive/recommend')) recommending.value = false
  }

  /** 推荐流程结束：无论成败都要解除进行中，否则按钮会一直转。 */
  function recommendSettled(): void {
    recommending.value = false
  }

  // ---- 运行日志 ----
  const lines = ref<LogLine[]>([])

  function push(text: string, level: LogLine['level'] = 'info'): void {
    const next = [...lines.value, { at: Date.now(), level, text }]
    // 超出上限丢最旧的：滚动区里翻几百行以前的记录没有意义。
    lines.value = next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next
  }

  function clear(): void {
    lines.value = []
  }

  // ---- 重跑交接 ----
  const pending = ref<RerunRequest | null>(null)

  function request(payload: RerunRequest): void {
    pending.value = payload
  }

  /** 取走待办。取过就清空：同一个请求不该被应用两次。 */
  function take(): RerunRequest | null {
    const value = pending.value
    pending.value = null
    return value
  }

  return {
    startScan,
    startSpeed,
    register,
    runStartScan,
    runStartSpeed,
    applied,
    suggestions,
    visibleSuggestions,
    recommending,
    applyApplied,
    applySuggestion,
    revert,
    accept,
    dismiss,
    mute,
    recommend,
    recommendSettled,
    reasonText,
    lines,
    push,
    clear,
    pending,
    request,
    take,
  }
})

/** 把时间戳格式化成 HH:MM:SS，日志里不需要日期。 */
export function formatLogTime(at: number): string {
  const date = new Date(at)
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}
