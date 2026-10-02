/**
 * ASN 与地理：库状态、代理出口提示、运营商快捷选项。
 *
 * 快捷选项的名字由后端下发，前端不硬编码——与导出字段清单同一个理由。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import type { GeoStatus } from '@/api/types'

export const useGeoStore = defineStore('geo', () => {
  const status = ref<GeoStatus | null>(null)
  /** 用户点过「不再提示」之后本次会话不再显示。 */
  const warningDismissed = ref(false)

  const loaded = computed(() => status.value?.status.loaded ?? false)
  const quickFilters = computed(() => status.value?.quick_filters ?? [])
  const warning = computed(() => {
    if (warningDismissed.value) return null
    return status.value?.warning ?? null
  })

  function apply(next: GeoStatus): void {
    const previousCountry = status.value?.warning?.country
    status.value = next
    // 换了一条新的提示就重新显示：上一次的「不再提示」只对那一条生效。
    if (next.warning?.country !== previousCountry) {
      warningDismissed.value = false
    }
  }

  function dismissWarning(): void {
    warningDismissed.value = true
  }

  return { status, loaded, quickFilters, warning, apply, dismissWarning }
})
