/**
 * 设置：全量替换，绝不做增量合并。
 *
 * 后端的 `settings` 事件与 `settings/get` 的响应是同一个形状，处理方式也
 * 完全一样——拿到就用它替换本地状态。增量合并正是旧项目「改了这边那边看
 * 不到、两边还互相盖掉」的根源。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import type {
  FieldIssue,
  HealthReport,
  NotifyConfig,
  ParamOrigins,
  Settings,
  SettingsPayload,
  UIConfig,
} from '@/api/types'
import { useUIStore } from './ui'

/** 改设置时带上的参数来源标记。用户手改的一律是 user。 */
export type Origin = 'default' | 'preset' | 'user'

export const useSettingsStore = defineStore('settings', () => {
  const values = ref<Settings | null>(null)
  const restartRequired = ref<string[]>([])
  const warnings = ref<FieldIssue[]>([])
  const loaded = ref(false)
  /** 最近一次体检结果。 */
  const health = ref<HealthReport | null>(null)

  const ui = computed<UIConfig | null>(() => values.value?.ui ?? null)
  const notify = computed<NotifyConfig | null>(() => values.value?.notify ?? null)
  const origins = computed<ParamOrigins>(() => values.value?.origins ?? {})

  /**
   * 按路径查一条告警。
   *
   * 界面逐项渲染时用它就地标警示色——代价要在改的那一刻说清楚，而不是等用户
   * 自己想起来去点「配置体检」。
   */
  const warningOf = (path: string): FieldIssue | undefined =>
    warnings.value.find((item) => item.key === path)

  /**
   * apply 全量替换本地设置，并顺带把界面相关的几项贴到 DOM 上。
   *
   * 顺带做这件事而不是让界面组件各自监听：界面配置与界面表现之间只应该有
   * 一个转换点，多一处就多一处会漏的地方。
   */
  function apply(payload: SettingsPayload): void {
    values.value = payload.values
    restartRequired.value = payload.restart_required ?? []
    warnings.value = payload.warnings ?? []
    loaded.value = true
    if (payload.values.ui) {
      useUIStore().applyFromSettings(payload.values.ui)
    }
  }

  /** patch 的返回值由调用方决定怎么用（这里只负责组包）。 */
  function patchEnvelope(patch: Record<string, unknown>, changed: string[] = []): {
    patch: Record<string, unknown>
    origins: ParamOrigins
  } {
    const marks: ParamOrigins = {}
    for (const key of changed) {
      marks[key] = 'user'
    }
    return { patch, origins: marks }
  }

  function applyHealth(report: HealthReport): void {
    health.value = report
  }

  function resetEnvelope(keys?: string[]): { keys: string[] } {
    return { keys: keys ?? [] }
  }

  return {
    values,
    ui,
    notify,
    origins,
    restartRequired,
    warnings,
    warningOf,
    loaded,
    health,
    apply,
    applyHealth,
    patchEnvelope,
    resetEnvelope,
  }
})
