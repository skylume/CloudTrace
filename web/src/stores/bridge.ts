/**
 * 接线层：把后端事件分发到各个 store。
 *
 * 全应用只有这一处调用 `onEvent`。组件各自订阅会让「谁该在断线后重新拉
 * 数据」这类问题散落到各处，也会让同一份事件被重复处理。
 */
import { api } from '@/api/rest'
import { EVT, onEvent, sendCommand, setSendFailureHandler, wsClient } from '@/api/client'
import { t } from '@/i18n'
import { notifyTaskEnd, type NotifyOutcome, type NotifyPrefs } from '@/utils/notify'

import { useAdaptiveStore, type AdaptiveNotice } from './adaptive'
import { useExportStore } from './export'
import { useGeoStore } from './geo'
import { useHistoryStore, type HistoryFilter, type LoadedHistory } from './history'
import { useLogStore } from './log'
import { useMigrateStore } from './migrate'
import { usePresetsStore } from './presets'
import { useResultsStore } from './results'
import { useSettingsStore } from './settings'
import { useSpeedStore, type BreakerNotice, type SourceDecision } from './speed'
import { useTaskStore } from './task'
import { useUIStore } from './ui'

import type { ErrorPayload, HistoryChangePayload, ProgressPayload } from '@/api/protocol'
import type {
  ExportResult,
  GeoStatus,
  HealthReport,
  IPRecord,
  MigrateStatus,
  PresetsPayload,
  SettingsPayload,
  TaskState,
} from '@/api/types'

/**
 * notifyEnd 任务刚结束时按配置提醒一次。
 *
 * 只处理「完成」与「失败」两种结束：中途停止是用户自己按的，他本来就知道，
 * 再提醒一次只是噪音。
 *
 * 文案在这里拼而不是在 notify 模块里：那个模块不认识语言，也不该认识——
 * 它只负责「用哪几个渠道把这段文字送出去」。
 *
 * **提醒里不带结果条数**：结果是一批批发过来的，而总线跨 topic 不保证到达
 * 顺序——这条提醒到达时，最后一批可能还在路上。报一个偏小的数字比不报更糟：
 * 用户会以为漏扫了。
 */
function notifyEnd(state: TaskState, prefs: NotifyPrefs | null): void {
  const outcome: NotifyOutcome | null =
    state.status === 'done' ? 'done' : state.status === 'failed' ? 'failed' : null
  if (!outcome) return

  const phase = t(`task.phase.${state.phase}`)
  let body: string
  if (outcome === 'done') {
    body = t('notify.body.done', { phase })
  } else {
    // 失败原因可能为空（比如进程被杀），那时给一句通用的话而不是「失败：」。
    body = state.error
      ? t('notify.body.failed', { phase, reason: state.error })
      : t('notify.body.failedUnknown', { phase })
  }

  notifyTaskEnd(outcome, prefs, {
    title: t(outcome === 'done' ? 'notify.title.done' : 'notify.title.failed'),
    body,
  })
}

/** refreshSettings 拉一次全量设置。重连之后必须重新拉，断线期间的改动补不回来。 */
export function refreshSettings(): void {
  sendCommand('settings/get')
}

/** refreshHistory 拉一次历史索引。 */
export function refreshHistory(filter?: HistoryFilter): void {
  const store = useHistoryStore()
  if (filter) store.filter = filter
  store.refresh()
}

export function refreshGeo(): void {
  sendCommand('geo/status')
}

/** refreshMigrate 拉一次旧版数据的迁移状态。 */
export function refreshMigrate(): void {
  useMigrateStore().refresh()
}

/** refreshPresets 拉一次档位列表。 */
export function refreshPresets(): void {
  usePresetsStore().refresh()
}

/**
 * wireEvents 建立事件 → store 的映射，并处理重连后的状态恢复。
 *
 * 重连成功时重新拉一遍全量状态：设置、历史、ASN 状态都是后端说了算，
 * 断线期间它们可能已经变了。
 */
export function wireEvents(): void {
  const task = useTaskStore()
  const settings = useSettingsStore()
  const results = useResultsStore()
  const history = useHistoryStore()
  const geo = useGeoStore()
  const ui = useUIStore()
  const log = useLogStore()
  const speed = useSpeedStore()
  const exporter = useExportStore()
  const adaptive = useAdaptiveStore()
  const presets = usePresetsStore()
  const migrate = useMigrateStore()

  setSendFailureHandler((type) => {
    ui.pushToast({ kind: 'warn', message: t('conn.lost') })
    console.warn(`[bridge] 命令 ${type} 未能发出：连接不可用`)
  })

  wsClient().onStateChange = (state, retrySeconds) => {
    task.setConnection(state, retrySeconds)
  }

  wsClient().onOpen(() => {
    // 首连与每次重连都会走到这里。
    refreshSettings()
    refreshPresets()
    refreshMigrate()
    refreshHistory()
    refreshGeo()
  })

  onEvent(EVT.state, (data) => {
    const next = data as TaskState
    const previous = task.state
    if (next.phase !== previous.phase || next.status !== previous.status) {
      log.push(t('task.' + next.status) + ' · ' + t('task.phase.' + next.phase))
    }
    if (previous.status === 'running') {
      // 跑完就呈现结果：停在扫描页的话，用户得自己想到「结果在另一个页面」，
      // 而这一步本来就是他要做的事。
      if (next.status === 'done') ui.activeView = 'result'
      // 中途停止但已经扫出了东西时也跳：那批结果同样是可用的。
      else if (next.status === 'aborted' && results.total > 0) ui.activeView = 'result'

      notifyEnd(next, settings.notify)
    }
    task.applyState(next)
  })
  onEvent(EVT.progress, (data) => task.applyProgress(data as ProgressPayload))

  onEvent(EVT.scanResult, (data) => results.addChunk((data as IPRecord[]) ?? []))
  onEvent(EVT.speedPartial, (data) => results.addChunk((data as IPRecord[]) ?? []))

  onEvent(EVT.export, (data) => exporter.applyResult(data as ExportResult))

  onEvent(EVT.adaptiveApplied, (data) => {
    const notice = data as AdaptiveNotice
    adaptive.applyApplied(notice)
    // 「智能推荐」的结果也是走这条事件回来的，收到就说明那一次结束了。
    adaptive.recommendSettled()
    // 日志里也留一条：徽标会随参数变化消失，而「它什么时候改过」之后还得能查到。
    log.push(t('adaptive.appliedLog', { key: notice.key, from: notice.from, to: notice.to }))
  })
  onEvent(EVT.adaptiveSuggestion, (data) => adaptive.applySuggestion(data as AdaptiveNotice))

  onEvent(EVT.speedSource, (data) => speed.applySource(data as SourceDecision))

  onEvent(EVT.speedBreaker, (data) => {
    const notice = data as BreakerNotice
    speed.applyBreaker(notice)
    // 日志里也留一条：提示条会被关掉，而「刚才为什么停了」之后还得能查到。
    log.push(notice.message, 'warn')
  })

  onEvent(EVT.settings, (data) => settings.apply(data as SettingsPayload))
  onEvent(EVT.presets, (data) => presets.apply(data as PresetsPayload))
  onEvent(EVT.migrate, (data) => migrate.apply(data as MigrateStatus))
  onEvent(EVT.geo, (data) => {
    const next = data as GeoStatus
    const before = geo.status?.status.records ?? 0
    geo.apply(next)
    if (next.status.records !== before) {
      log.push(t('geo.records') + ' ' + String(next.status.records))
    }
  })

  onEvent(EVT.historyList, (data) => {
    const payload = data as { entries?: unknown[]; total?: number }
    history.applyList((payload.entries ?? []) as never[], payload.total ?? 0)
  })
  onEvent(EVT.historyLoad, (data) => history.applyLoaded(data as LoadedHistory))
  onEvent(EVT.historyGet, (data) => {
    const payload = data as { record?: unknown }
    history.applyDetail((payload.record ?? null) as never)
  })
  onEvent(EVT.historyDelete, (data) => {
    const payload = data as { id?: string; undo_ms?: number }
    if (payload.undo_ms) history.applyUndoWindow(payload.undo_ms)
  })
  onEvent(EVT.historyCompare, (data) => {
    const payload = data as { diff?: unknown }
    if (payload.diff) history.applyDiff(payload.diff as never)
  })
  onEvent(EVT.historyClear, (data) => {
    const payload = data as { removed?: number }
    ui.pushToast({ kind: 'ok', message: t('settings.clearHistoryDone', { count: payload.removed ?? 0 }) })
    refreshHistory()
  })
  onEvent(EVT.historyChanged, (data) => {
    const change = data as HistoryChangePayload
    // 变更事件只带 id 与动作，列表要重新拉一次：本地拼不出准确的顺序，
    // 也算不出保留策略会把哪一条挤掉。
    log.push(t('history.title') + ' · ' + change.action)
    refreshHistory()
  })

  onEvent(EVT.error, (data) => {
    const payload = data as ErrorPayload
    const message = errorText(payload)
    ui.pushToast({ kind: 'bad', message })
    log.push(message, 'bad')
    // 出错时也要解除等待态，否则按钮会一直转下去。
    exporter.fail()
    adaptive.recommendSettled()
    migrate.fail()
  })

  onEvent(EVT.health, (data) => settings.applyHealth(data as HealthReport))
}

/**
 * errorText 把错误码翻成人话。
 *
 * 后端给的 msg 已经是中文人话，直接用它；错误码只用来决定提示的语气与
 * 是否需要额外动作。
 */
export function errorText(payload: ErrorPayload): string {
  const known = `error.${payload.code}`
  const translated = t(known as never)
  if (translated !== known) return translated
  return payload.msg || t('error.E_UNKNOWN')
}

/** bootstrap 一次性把应用跑起来。 */
export function bootstrap(): void {
  wireEvents()
  wsClient().connect()
  // 探活接口不鉴权，可以早于 WS 拿到版本与数据目录，用于首屏与错误页。
  void api.health().catch(() => {
    /* 探活失败不影响主流程，连接状态由 WS 反映 */
  })
}
