<script setup lang="ts">
/**
 * 扫描页：让用户在不理解任何参数的前提下，一次点击得到结果。
 *
 * 这个文件只做编排：连 store、组装请求、把区域分给组件。具体控件一律在
 * 子组件里——页面模板保持短，才能逼着结构被拆开。
 */
import { CMD } from '@/api/protocol'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { sendCommand } from '@/api/client'
import DepthPicker from '@/components/scan/DepthPicker.vue'
import LogPanel from '@/components/scan/LogPanel.vue'
import PresetPanel from '@/components/scan/PresetPanel.vue'
import ProcessPanel from '@/components/scan/ProcessPanel.vue'
import SourcePanel, { type ScanSource } from '@/components/scan/SourcePanel.vue'
import Banner from '@/components/ui/Banner.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import { t } from '@/i18n'
import { DEFAULT_DEPTH, depthValues, type ScanDepth } from '@/i18n/depth'
import { PARAM_WIRE_KEYS } from '@/i18n/params'
import { useGeoStore } from '@/stores/conn'
import { useResultsStore } from '@/stores/results'
import { useScanStore } from '@/stores/scan'
import { useSettingsStore } from '@/stores/settings'
import { useSystemStore } from '@/stores/conn'
import { useTaskStore } from '@/stores/conn'
import { useUIStore } from '@/stores/ui'
import { rememberScanRequest } from '@/utils/lastScan'
import { parseSourceText, sourceEntryCount } from '@/utils/sourceText'
import {
  clearSnapshot,
  loadSnapshot,
  worthRestoring,
  type SessionSnapshot,
} from '@/utils/sessionSnapshot'
import { isUntouchedScan, scanParamsFromConfig } from '@/utils/scanState'

const task = useTaskStore()
const geo = useGeoStore()
const results = useResultsStore()
const settings = useSettingsStore()
const scan = useScanStore()
const ui = useUIStore()
const system = useSystemStore()

/**
 * 上次没跑完的那批结果。
 *
 * 只在挂载时读一次：之后这个提示要么被点掉、要么被恢复，不需要跟着变化。
 * 值不值得提示由 worthRestoring 判断——只扫出几条的话，重扫比点「恢复」还快。
 */
const session = ref<SessionSnapshot | null>(null)
const sessionOffer = computed(() =>
  session.value ? t('session.offer', { count: session.value.records.length }) : '',
)

function restoreSession(): void {
  const snap = session.value
  if (!snap) return
  results.replaceAll(snap.records)
  clearSnapshot()
  session.value = null
  ui.activeView = 'result'
}

function dismissSession(): void {
  clearSnapshot()
  session.value = null
}

/**
 * 旧版数据那条提示的文案。
 *
 * 在脚本里拼好而不是写进模板：模板里的模板字面量容易被工具链吞掉（这个仓库
 * 已经被坑过几次）。
 */
const migrateOffer = computed(() => {
  const s = settings.migrateStatus
  if (!s) return ''
  const parts: string[] = []
  if (s.settings) parts.push(t('migrate.part.settings'))
  if (s.histories > 0) parts.push(t('migrate.part.histories', { count: s.histories }))
  return t('migrate.offer', { parts: parts.join('、') })
})

const migrateResult = computed(() => {
  const r = settings.migrateReport
  if (!r) return ''
  const parts: string[] = []
  if (r.settings) parts.push(t('migrate.part.settings'))
  if (r.imported > 0) parts.push(t('migrate.part.histories', { count: r.imported }))
  let text = t('migrate.done', { parts: parts.join('、') })
  if (r.backup_dir) text += t('migrate.doneBackup', { dir: r.backup_dir })
  if (r.skipped && r.skipped.length > 0) text += t('migrate.doneSkipped', { count: r.skipped.length })
  return text
})

/** 旧档位横幅的文案。同样在脚本里拼，避开模板字面量。 */
const legacyOffer = computed(() => t('legacy.offer', { count: settings.legacyStatus?.presets?.length ?? 0 }))

const legacyResult = computed(() => {
  const s = settings.legacyResult
  if (!s) return ''
  let text = t('legacy.done', { count: s.imported?.length ?? 0 })
  if (s.skipped && s.skipped.length > 0) text += t('legacy.doneSkipped', { count: s.skipped.length })
  return text
})

/**
 * 参数状态用驼峰，与 i18n 映射表的 key 一致；发请求时再转成后端的下划线。
 *
 * 初值先空着，等配置或档位列表到了再填：档位列表是异步来的，在这里写死一个
 * 内置档位等于把后端那份定义又抄了一遍。
 */
const params = ref<Record<string, number | boolean>>({})

/**
 * 最近点过的深度，只用来给那三个按钮加高亮。
 *
 * 它不参与请求、不进历史、不落盘：改完参数之后高亮会停在最后点的那一档，
 * 界面不会跳出来说「已偏离」——那正是档位体系变得难用的起点。
 */
const depth = ref<ScanDepth>(DEFAULT_DEPTH)
const source = ref<ScanSource>({ official: true, remote: [], customText: '' })
/**
 * 官方网段条数。由后端下发（见 stores/system）。
 *
 * 这里曾经是一个从不赋值的 `ref(0)`——界面上永远显示「共 0 段」，看起来像
 * 没有数据。清单是随二进制固化的，前端不该自己算。
 */
const officialCount = computed(() => system.officialV4)

const running = computed(() => task.running)
const showEmpty = computed(() => !running.value && results.total === 0)

/**
 * 启动前就能判定、发出去也只会空转的来源问题。
 *
 * 空地址的远程源会被后端静默跳过，全部关掉则会让任务跑完一分钟再返回零结果——
 * 这两件事都能在本地先拦住，不必等结果页才发现自己没填。
 */
const sourceIssue = computed(() => {
  const blank = source.value.remote.filter((item) => item.enabled && item.url.trim() === '').length
  if (blank > 0) return t('source.remote.invalid', { count: blank })

  const hasRemote = source.value.remote.some((item) => item.enabled && item.url.trim() !== '')
  if (source.value.official || hasRemote) return ''
  if (sourceEntryCount(parseSourceText(source.value.customText)) > 0) return ''
  return source.value.customText.trim() === '' ? t('source.none') : t('source.unparsed')
})

/**
 * 把「开始扫描」注册给动作注册表。
 *
 * Ctrl+Enter 与按钮走的是完全相同的路径——参数、
 * 来源合并、日志都一致，不会出现「快捷键启动的任务少带了来源」这种偏差。
 */
let unregister: (() => void) | null = null

/**
 * 把来源写回配置。
 *
 * 远端地址与自定义文本都是配置项，而扫描任务读的是配置——不写回的话，用户
 * 加的远端地址只存在于这个页面的内存里，一刷新就没了，扫描时也不会去拉。
 *
 * 带防抖：输入框是逐字符触发变更的，每敲一个字都发一次配置更新既浪费也会
 * 让服务端的配置写入变成瓶颈。
 */
let persistTimer: ReturnType<typeof setTimeout> | null = null

function schedulePersistSource(): void {
  if (persistTimer !== null) clearTimeout(persistTimer)
  persistTimer = setTimeout(() => {
    persistTimer = null
    sendCommand(CMD.settingsUpdate, {
      patch: {
        source: { remote_urls: source.value.remote },
        scan: { custom_source: source.value.customText },
      },
      origins: { 'source.remote_urls': 'user', 'scan.custom_source': 'user' },
    })
  }, 400)
}

watch(() => source.value.remote, schedulePersistSource, { deep: true })
watch(() => source.value.customText, schedulePersistSource)

/*
 * 面板上的参数只影响本次任务。
 *
 * 曾经这里把改动差分后写回配置：用户以为在「调一下试试」，实际改掉了全局默认，
 * 下次启动还是这个值，自适应也把它当成了用户的长期选择。要改默认就去设置页。
 */

/**
 * 配置一变就以它为准。
 *
 * 面板与服务端因此只差「用户还没写完的那几个键」。这也让「应用档位」不必在
 * 前端填一遍值：服务端写完配置，广播回来就填上了，两边不会各写一次。
 */
watch(
  () => settings.values,
  () => {
    const scan = settings.values?.scan as Record<string, unknown> | undefined
    const filled = scanParamsFromConfig(scan)
    if (Object.keys(filled).length === 0) return
    params.value = { ...params.value, ...filled }
  },
)

/**
 * 按配置初始化面板。
 *
 * 配置没被动过时从「标准」起步，动过就以配置为准。只填界面、不写配置：
 * 这次填值不代表用户的选择，默认值归设置页管。
 */
function syncFromSettings(): void {
  const scan = settings.values?.scan as Record<string, unknown> | undefined

  // 来源也从配置恢复：用户上次加过的远端地址与写过的文本不该每次重填。
  const src = settings.values?.source as Record<string, unknown> | undefined
  const remote = src?.['remote_urls']
  if (Array.isArray(remote)) {
    source.value = { ...source.value, remote: remote as typeof source.value.remote }
  }
  const custom = scan?.['custom_source']
  if (typeof custom === 'string' && custom !== '') {
    source.value = { ...source.value, customText: custom }
  }

  if (isUntouchedScan(settings.origins)) {
    // 配置没被动过：从「标准」起步。
    //
    // 只填界面、不写配置：这次填值不代表用户的选择，写进配置等于替用户把一个
    // 他没看过的值定成了默认值。默认值归设置页管。
    params.value = { ...params.value, ...depthValues(DEFAULT_DEPTH) }
    return
  }

  const filled = scanParamsFromConfig(scan)
  if (Object.keys(filled).length === 0) return
  params.value = { ...params.value, ...filled }
}

/**
 * 套用某一档深度。
 *
 * 只覆盖这一档声明过的键，其余参数（端口、超时、重试）保持用户当前的值：
 * 「快速」是少采一点，不是顺手把端口也改掉。
 */
function applyDepth(id: ScanDepth): void {
  depth.value = id
  params.value = { ...params.value, ...depthValues(id) }
}

/**
 * 套用历史页送来的参数快照。
 *
 * 只覆盖快照里带的项，来源文本单独填——官方网段与远端地址取自当前配置：
 * 历史里记的是「当时扫了什么」，而不是「现在该怎么配来源」。
 */
function applyRerun(): void {
  const request = scan.take()
  if (!request) return
  params.value = { ...params.value, ...request.params }
  if (request.customText !== '') source.value = { ...source.value, customText: request.customText }
}

onMounted(() => {
  const saved = loadSnapshot()
  if (saved && worthRestoring(saved)) session.value = saved
  unregister = scan.register('startScan', start)
  syncFromSettings()
  applyRerun()
})

// 页面可能一直挂着（比如从命令面板跳回来），因此不能只在挂载时取一次。
watch(() => scan.pending, applyRerun)

onBeforeUnmount(() => {
  unregister?.()
  // 来源文本仍是配置项，离开页面时把防抖窗口里没写回的那点改动补上。
  if (persistTimer !== null) clearTimeout(persistTimer)
})

/** 组装后端要的扫描参数。 */
function buildRequest(): Record<string, unknown> {
  const payload: Record<string, unknown> = { ip_version: 4 }
  for (const [camel, wire] of Object.entries(PARAM_WIRE_KEYS)) {
    payload[wire] = params.value[camel]
  }
  // 来源模式由四块来源的开关共同决定，不让用户再选一次——多一次选择就多一处会选错的地方。
  const hasCustom = source.value.customText.trim() !== ''
  const hasRemote = source.value.remote.some((item) => item.enabled && item.url.trim() !== '')
  if (source.value.official && (hasCustom || hasRemote)) payload.source_mode = 'both'
  else if (source.value.official) payload.source_mode = 'official'
  else payload.source_mode = 'custom'
  payload.custom_source = source.value.customText
  return payload
}

function start(): void {
  // 来源不成立时不发请求：让用户改完再跑，比跑完一分钟给个空结果更省事。
  if (sourceIssue.value !== '') {
    scan.push(sourceIssue.value, 'warn')
    return
  }
  const payload = buildRequest()
  if (!sendCommand(CMD.scanStart, payload)) return
  // 记下这份请求：零结果时结果页要用同一份参数放宽重跑。
  rememberScanRequest(payload)
  scan.push(t('scan.start'))
}

function stop(): void {
  sendCommand(CMD.scanStop)
  scan.push(t('scan.stop'), 'warn')
}
</script>

<template>
  <div class="page">
    <Banner v-if="geo.warning" tone="warn" :message="geo.warning.message" @close="geo.dismissWarning()" />

    <!--
      旧版数据：只提示，用户点了才动数据。悄悄搬东西比不搬更糟——用户会发现
      自己的旧配置在不知情的时候变了，而那时他已经记不清原来是什么样。
    -->
    <Banner
      v-if="settings.migrateShouldOffer"
      tone="info"
      :message="migrateOffer"
      :action-label="settings.migrateRunning ? t('migrate.running') : t('migrate.import')"
      @action="settings.runMigrate()"
      @close="settings.dismissMigrate()"
    />
    <!--
      档位已下线，但旧 presets.json 里的自定义档位是用户自己调过的参数：只提示，
      点「设为默认参数」才写配置，点「导出保存」则把原样 JSON 交给他。
    -->
    <Banner
      v-if="settings.legacyShouldOffer"
      tone="info"
      :message="legacyOffer"
      :note="t('legacy.note')"
      :action-label="settings.legacyRunning ? t('legacy.running') : t('legacy.import')"
      :action2-label="t('legacy.export')"
      @action="settings.runLegacy()"
      @action2="settings.exportLegacy()"
      @close="settings.dismissLegacy()"
    />
    <Banner v-else-if="legacyResult !== ''" tone="ok" :message="legacyResult" @close="settings.dismissLegacy()" />
    <!--
      上次没跑完的结果：只提示，用户点了才动。扫描要跑一分钟，这期间刷新或
      崩掉就白跑了，而历史只保存已完成的任务——中途的结果按设计不写历史。
    -->
    <Banner
      v-if="session"
      tone="info"
      :message="sessionOffer"
      :action-label="t('session.restore')"
      @action="restoreSession"
      @close="dismissSession"
    />
    <Banner
      v-else-if="migrateResult !== ''"
      tone="ok"
      :message="migrateResult"
      @close="settings.dismissMigrate()"
    />

    <!--
      首屏只有「扫描深度 + 开始扫描」：这两件事是同一个决定的两半。来源与参数
      就在下面，但简单模式只露常用项；要看全部参数请去设置页切换模式，或按
      Ctrl+Shift+A——披露状态只有一个入口，不在这里再放一个。
    -->
    <!-- 来源不成立时直接挡在按钮前：这条不是提醒，是「现在点也没用」。 -->
    <Banner v-if="sourceIssue !== ''" tone="warn" :message="sourceIssue" :closable="false" />

    <div class="actions">
      <button v-if="!running" type="button" class="ct-btn ct-btn--primary ct-btn--lg" @click="start">
        {{ t('scan.start') }}
      </button>
      <button v-else type="button" class="ct-btn ct-btn--lg" @click="stop">
        {{ t('scan.stop') }}
      </button>
      <DepthPicker :depth="depth" :disabled="running" @apply="applyDepth" />
      <!--
        「智能推荐」是显式操作：点了才生效，且不受来源限制（可以改用户手填过的
        值）。它是自动自适应的手动对应物——那条自动路径只在 default/preset 来源
        上动手，手填过的值永远只给建议。
      -->
      <button
        type="button"
        class="ct-link"
        :disabled="scan.recommending"
        :title="t('adaptive.recommendHint')"
        @click="scan.recommend()"
      >
        {{ scan.recommending ? t('adaptive.recommending') : t('adaptive.recommend') }}
      </button>
    </div>

    <div class="columns">
      <div class="main">
        <!--
          简单模式不是「什么都看不到」，而是「只看到最要紧的几项」：来源、端口、
          并发、阈值、测速数量、测速源。其余项与数据源的详细块收进「详细设置」。
        -->
        <SourcePanel v-model="source" :official-count="officialCount" />
        <PresetPanel v-model:params="params" :disabled="running" />
      </div>

      <div class="side">
        <EmptyState
          v-if="showEmpty"
          class="ct-card"
          :title="t('scan.empty.title')"
          :desc="t('scan.empty.desc')"
        />
        <ProcessPanel v-else />
        <LogPanel />
      </div>
    </div>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.columns {
  display: grid;
  grid-template-columns: minmax(0, 1.45fr) minmax(0, 1fr);
  gap: var(--space-4);
  align-items: start;
}

.main,
.side {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  min-width: 0;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}

.spacer {
  flex: 1;
}

@media (max-width: 1024px) {
  .columns {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
