/**
 * 历史：列表只读索引，详情才读记录文件。
 *
 * 索引里放够了列表要显示的字段，因此「列个表」不发任何读文件的请求——
 * 这是历史体系最要紧的一条性能约束，前端也要守住它。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import type { HistoryIndexEntry, HistoryRecord, ParamDiff } from '@/api/types'

export interface HistoryFilter {
  type?: 'scan' | 'speed'
  ip_version?: number
  keyword?: string
  tag?: string
  starred?: boolean
  limit?: number
  offset?: number
}

export interface LoadedHistory {
  record: HistoryRecord
  paramDiff: ParamDiff[]
  ageMinutes: number
}

export const useHistoryStore = defineStore('history', () => {
  const entries = ref<HistoryIndexEntry[]>([])
  const total = ref(0)
  const filter = ref<HistoryFilter>({})
  const loaded = ref<LoadedHistory | null>(null)

  const starred = computed(() => entries.value.filter((entry) => entry.starred))
  const regions = computed(() => {
    const set = new Set<string>()
    for (const entry of entries.value) {
      for (const code of entry.regions ?? []) set.add(code)
    }
    return [...set].sort()
  })

  function applyList(list: HistoryIndexEntry[], count: number): void {
    entries.value = list
    total.value = count
  }

  function applyLoaded(payload: LoadedHistory): void {
    loaded.value = payload
  }

  /** 变更事件只带 id 与动作，列表要重新拉一次——本地拼不出准确的顺序。 */
  function markStale(): void {
    // 具体刷新由接线层触发，这里只留一个可观测的标记位。
    stale.value = true
  }

  const stale = ref(false)

  return { entries, total, filter, loaded, starred, regions, stale, applyList, applyLoaded, markStale }
})
