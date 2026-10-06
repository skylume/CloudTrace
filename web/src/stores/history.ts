/**
 * 历史：列表只读索引，详情才读记录文件。
 *
 * 索引里放够了列表要显示的字段，因此「列个表」不发任何读文件的请求——这是
 * 历史体系最要紧的一条性能约束，前端也要守住它：列表页不调 history/get。
 *
 * 删除走软删除 + 撤销窗口，窗口长度由后端给（不写死在前端）：两端对窗口长度
 * 的认知必须一致，否则会出现「撤销按钮还在、后端已经落定」的情况。
 */
import { CMD } from '@/api/protocol'
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { sendCommand } from '@/api/client'
import type { HistoryDiff, HistoryIndexEntry, HistoryRecord, ParamDiff } from '@/api/types'

export interface HistoryFilter {
  type?: string
  ip_version?: number
  region?: string
  tags?: string[]
  starred?: boolean
  min_count?: number
  max_count?: number
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
  const diff = ref<HistoryDiff | null>(null)
  /**
   * 抽屉里正在看的那一份。
   *
   * 与 `loaded` 分开：`loaded` 是「已经送进结果集的那一份」，而抽屉只是看一眼。
   * 合成一个的话，点开抽屉就等于换了结果集——用户没这个预期。
   */
  const detail = ref<HistoryRecord | null>(null)
  /**
   * 撤销窗口（毫秒）。
   *
   * 由后端在删除响应里给，前端不写死：两端对窗口长度的认知必须一致，否则会
   * 出现「撤销按钮还在、后端已经落定」的情况。
   */
  const undoWindowMs = ref(15000)

  const starred = computed(() => entries.value.filter((entry) => entry.starred))
  const allTags = computed(() => {
    const set = new Set<string>()
    for (const entry of entries.value) {
      for (const tag of entry.tags ?? []) set.add(tag)
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

  function applyDiff(next: HistoryDiff): void {
    diff.value = next
  }

  function applyDetail(record: HistoryRecord | null): void {
    detail.value = record
  }

  function applyUndoWindow(ms: number): void {
    if (ms > 0) undoWindowMs.value = ms
  }

  /** 拉列表。这是列表页唯一会发出的请求。 */
  function refresh(): void {
    sendCommand(CMD.historyList, { filter: filter.value })
  }

  /**
   * 加载一份历史。
   *
   * current 由前端带上：用户可能改完参数还没保存就来加载历史，服务端从配置里
   * 取会给出与实际不符的差异结论。
   */
  function load(id: string, current: unknown): void {
    sendCommand(CMD.historyLoad, { id, current })
  }

  /**
   * 读一份完整记录。
   *
   * 列表页刻意不调它：索引里已经放够了列表要显示的字段，读记录文件是「用户
   * 明确要看某一份」之后的事。抽屉打开时才走这条路。
   */
  function get(id: string): void {
    sendCommand(CMD.historyGet, { id })
  }

  function closeDetail(): void {
    detail.value = null
  }

  /**
   * 清空全部历史。
   *
   * 没有撤销窗口：撤销是给「点错了一个」准备的，而清空是明确要求把整个列表
   * 抹掉。确认由界面负责。
   */
  function clear(): void {
    sendCommand(CMD.historyClear, {})
  }

  function remove(id: string): void {
    sendCommand(CMD.historyDelete, { id })
  }

  function undo(id: string): void {
    sendCommand(CMD.historyUndo, { id })
  }

  function saveTags(id: string, patch: { tags?: string[]; note?: string; starred?: boolean }): void {
    const entry = entries.value.find((item) => item.id === id)
    sendCommand(CMD.historyTag, {
      id,
      tags: patch.tags ?? entry?.tags ?? [],
      note: patch.note ?? entry?.note ?? '',
      starred: patch.starred ?? entry?.starred ?? false,
    })
  }

  function compare(idA: string, idB: string): void {
    sendCommand(CMD.historyCompare, { idA, idB })
  }

  return {
    entries,
    total,
    filter,
    loaded,
    diff,
    detail,
    undoWindowMs,
    starred,
    allTags,
    applyList,
    applyLoaded,
    applyDiff,
    applyDetail,
    applyUndoWindow,
    refresh,
    get,
    clear,
    closeDetail,
    load,
    remove,
    undo,
    saveTags,
    compare,
  }
})
