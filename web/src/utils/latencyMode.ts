/**
 * 当前结果该用哪套延迟阈值分档。
 *
 * HTTPing 的 RTT 比 TCP 握手长，后端判「延迟达标」时按倍率放宽；前端着色若
 * 不跟着放宽，同一个节点会在表格里显示「慢」、却被后端算作合格。
 *
 * 历史结果不归一化：旧记录不知道当时的探测模式，硬按当前配置着色等于编造
 * 一个它没有的结论，所以退回原始延迟并在信号条上注明。
 */
import { computed, type ComputedRef } from 'vue'

import { readPath } from '@/i18n/settingsSchema'
import { useResultsStore } from '@/stores/results'
import { useSettingsStore } from '@/stores/settings'

import type { LatencyMode } from './latency'

export type LatencyScale = LatencyMode | 'raw'

export function useLatencyScale(): ComputedRef<LatencyScale> {
  const settings = useSettingsStore()
  const results = useResultsStore()

  return computed<LatencyScale>(() => {
    if (results.sourceId !== '') return 'raw'
    const values = settings.values
    if (!values) return 'tcping'
    const mode = readPath(values as unknown as Record<string, unknown>, 'scan.mode')
    return mode === 'httping' ? 'httping' : 'tcping'
  })
}
