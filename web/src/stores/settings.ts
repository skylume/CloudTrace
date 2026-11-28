/**
 * 设置与旧数据：全量设置、旧版数据迁移、已下线档位的去向。
 *
 * 后端的 `settings` 事件与 `settings/get` 的响应是同一个形状，处理方式也
 * 完全一样——拿到就用它替换本地状态。增量合并正是旧项目「改了这边那边看
 * 不到、两边还互相盖掉」的根源。
 *
 * 迁移与旧档位都只在扫描页上提示，用户点了才动数据；它们的「忽略」只作用于
 * 本次会话，所以和设置放在一起，但各自用前缀区分状态，避免同名互相盖掉。
 */
import { CMD } from '@/api/protocol'
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { sendCommand } from '@/api/client'
import type {
  FieldIssue,
  HealthReport,
  LegacyPresetsStatus,
  MigrateStatus,
  NotifyConfig,
  ParamOrigins,
  Settings,
  SettingsPayload,
  UIConfig,
} from '@/api/types'
import { pickOutputTarget } from '@/utils/outputTarget'
import { useUIStore } from './ui'

/** 改设置时带上的参数来源标记。用户手改的一律是 user。 */
export type Origin = 'default' | 'preset' | 'user'

export const useSettingsStore = defineStore('settings', () => {
  // ---- 设置 ----
  const values = ref<Settings | null>(null)
  /** 当前运行形态：桌面版窗口内为 true，浏览器面板为 false。 */
  const desktop = ref(false)
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
    desktop.value = payload.desktop ?? false
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

  // ---- 旧版数据迁移 ----
  const migrateStatus = ref<MigrateStatus | null>(null)
  /** 用户点了「忽略」。只作用于本次会话：重启之后还会再问一次。 */
  const migrateDismissed = ref(false)
  /** 正在迁移，用于禁用按钮避免重复点击。 */
  const migrateRunning = ref(false)
  /**
   * 本次会话里刚跑过一次迁移。
   *
   * 结果横幅只在这个时候显示：后端的最近一次结果在内存里，刷新页面就会重新
   * 下发，不加这个标记的话用户每次刷新都会看到一条「已导入」。
   */
  const migrateJustRan = ref(false)

  /**
   * 是否该显示迁移横幅。
   *
   * 已经迁移过的旧数据不再提示：用户可能因为别的原因留着旧目录（比如还没删
   * 旧版本），每次都问一遍很烦。
   */
  const migrateShouldOffer = computed(
    () =>
      migrateStatus.value?.found === true &&
      !migrateStatus.value.migrated &&
      !migrateDismissed.value,
  )

  /** 刚跑完那次迁移的结果，供界面显示。 */
  const migrateReport = computed(() =>
    migrateJustRan.value && !migrateDismissed.value ? migrateStatus.value?.report : undefined,
  )

  function applyMigrate(next: MigrateStatus): void {
    migrateStatus.value = next
    if (next.report) {
      // 迁移已经跑过，等待态解除。
      migrateRunning.value = false
    }
  }

  function refreshMigrate(): void {
    sendCommand(CMD.migrateStatus)
  }

  function runMigrate(): void {
    if (migrateRunning.value) return
    migrateRunning.value = true
    migrateJustRan.value = true
    if (!sendCommand(CMD.migrateRun)) migrateRunning.value = false
  }

  /** 迁移失败时也要解除等待，否则按钮会一直转下去。 */
  function failMigrate(): void {
    migrateRunning.value = false
  }

  function dismissMigrate(): void {
    migrateDismissed.value = true
  }

  // ---- 旧档位 ----
  const legacyStatus = ref<LegacyPresetsStatus | null>(null)
  /** 用户点了「忽略」。只作用于本次会话：重启之后还会再问一次。 */
  const legacyDismissed = ref(false)
  /** 正在导入，用于禁用按钮避免重复点击。 */
  const legacyRunning = ref(false)
  /** 本次会话里刚导入过：结果横幅只在这时显示，刷新页面不重复报「已导入」。 */
  const legacyJustRan = ref(false)

  const legacyShouldOffer = computed(
    () =>
      legacyStatus.value?.found === true &&
      !legacyStatus.value.migrated &&
      (legacyStatus.value.presets?.length ?? 0) > 0 &&
      !legacyDismissed.value,
  )

  /** 刚跑完那次导入的结果，供界面显示。 */
  const legacyResult = computed(() => (legacyJustRan.value ? legacyStatus.value : null))

  function applyLegacy(next: LegacyPresetsStatus): void {
    legacyStatus.value = next
    if (next.imported?.length || next.skipped?.length) legacyRunning.value = false
  }

  function refreshLegacy(): void {
    sendCommand(CMD.legacyPresets)
  }

  function runLegacy(): void {
    if (legacyRunning.value) return
    legacyRunning.value = true
    legacyJustRan.value = true
    // 空 ids = 全部导入：横幅上只有一个动作，没必要再让用户挑一遍。
    if (!sendCommand(CMD.legacyPresetsImport, { ids: [] })) legacyRunning.value = false
  }

  /** 导入失败时也要解除等待，否则按钮会一直转下去。 */
  function failLegacy(): void {
    legacyRunning.value = false
  }

  function dismissLegacy(): void {
    legacyDismissed.value = true
  }

  /**
   * exportLegacy 把旧档位原样存成 JSON。
   *
   * 导出的是 `{default, presets}` 而不是当前配置：用户要的是「我原来调了什么」，
   * 翻译成新参数会丢掉新版已经不认识的项。
   */
  function exportLegacy(): void {
    const s = legacyStatus.value
    if (!s?.presets?.length) return
    const raw = JSON.stringify({ default: s.default ?? '', presets: s.presets }, null, 2)
    const blob = new Blob([raw], { type: 'application/json' })
    const name = 'cloudtrace-presets-legacy.json'
    pickOutputTarget({ name, blob }).save({ name, blob })
  }

  return {
    values,
    desktop,
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
    migrateStatus,
    migrateDismissed,
    migrateRunning,
    migrateShouldOffer,
    migrateReport,
    applyMigrate,
    refreshMigrate,
    runMigrate,
    failMigrate,
    dismissMigrate,
    legacyStatus,
    legacyDismissed,
    legacyRunning,
    legacyShouldOffer,
    legacyResult,
    applyLegacy,
    refreshLegacy,
    runLegacy,
    failLegacy,
    dismissLegacy,
    exportLegacy,
  }
})
