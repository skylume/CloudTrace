/**
 * 自适应状态。
 *
 * 两类信息分开存：
 *   - **已调整**：值真的变了，界面要给徽标与「还原」，让用户看得见、改得回
 *   - **建议**：值没变（用户的显式选择），等他自己点「一键应用」
 *
 * 分开的理由是规格里那条红线：自动逻辑绝不覆盖用户的显式选择。两类混在一起
 * 存，迟早会有某处把建议当成已调整来处理，而那种错误是静默的——值被改了，
 * 用户不知道为什么。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { sendCommand } from '@/api/client'
import { t } from '@/i18n'
import { buildPatch } from '@/i18n/settingsSchema'

export interface AdaptiveNotice {
  key: string
  from: number
  to: number
  /** 机器可读的原因标识，文案由界面层决定。 */
  reason: string
}

export const useAdaptiveStore = defineStore('adaptive', () => {
  /** 被自动调整过的参数：key → 调整详情。 */
  const applied = ref<Record<string, AdaptiveNotice>>({})
  /** 只给建议、值未变的参数。 */
  const suggestions = ref<AdaptiveNotice[]>([])
  /** 用户点过「不再提示」的建议原因。 */
  const muted = ref<string[]>([])

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
    sendCommand('settings/update', {
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
    sendCommand('settings/update', {
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

  return {
    applied,
    suggestions,
    visibleSuggestions,
    applyApplied,
    applySuggestion,
    revert,
    accept,
    dismiss,
    mute,
    reasonText,
  }
})
