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
import LogPanel from '@/components/scan/LogPanel.vue'
import PresetPanel from '@/components/scan/PresetPanel.vue'
import ProcessPanel from '@/components/scan/ProcessPanel.vue'
import SourcePanel, { type ScanSource } from '@/components/scan/SourcePanel.vue'
import Banner from '@/components/ui/Banner.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import { t } from '@/i18n'
import { CUSTOM_PRESET, PARAM_WIRE_KEYS, matchPreset, presetValues } from '@/i18n/params'
import PresetPicker from '@/components/scan/PresetPicker.vue'
import { useActionStore } from '@/stores/actions'
import { useRerunStore } from '@/stores/rerun'
import { useAdaptiveStore } from '@/stores/adaptive'
import { useGeoStore } from '@/stores/geo'
import { useLogStore } from '@/stores/log'
import { useMigrateStore } from '@/stores/migrate'
import {
  clearSnapshot,
  loadSnapshot,
  worthRestoring,
  type SessionSnapshot,
} from '@/utils/sessionSnapshot'
import { usePresetsStore } from '@/stores/presets'
import { useResultsStore } from '@/stores/results'
import { useSettingsStore } from '@/stores/settings'
import { useTaskStore } from '@/stores/task'
import { useUIStore } from '@/stores/ui'
import { diffScanParams, isUntouchedScan, scanParamsFromConfig } from '@/utils/scanState'

const task = useTaskStore()
const geo = useGeoStore()
const log = useLogStore()
const results = useResultsStore()
const settings = useSettingsStore()
const actions = useActionStore()
const presets = usePresetsStore()
const ui = useUIStore()
const rerun = useRerunStore()
const adaptive = useAdaptiveStore()
const migrate = useMigrateStore()

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
  const s = migrate.status
  if (!s) return ''
  const parts: string[] = []
  if (s.settings) parts.push(t('migrate.part.settings'))
  if (s.histories > 0) parts.push(t('migrate.part.histories', { count: s.histories }))
  return t('migrate.offer', { parts: parts.join('、') })
})

const migrateResult = computed(() => {
  const r = migrate.justRanReport
  if (!r) return ''
  const parts: string[] = []
  if (r.settings) parts.push(t('migrate.part.settings'))
  if (r.imported > 0) parts.push(t('migrate.part.histories', { count: r.imported }))
  let text = t('migrate.done', { parts: parts.join('、') })
  if (r.backup_dir) text += t('migrate.doneBackup', { dir: r.backup_dir })
  if (r.skipped && r.skipped.length > 0) text += t('migrate.doneSkipped', { count: r.skipped.length })
  return text
})

/** 展开 / 收起详细设置。与参数面板的「全部参数」共用同一个状态，不会各说各话。 */
function toggleDetails(): void {
  ui.setDensity(ui.showAdvanced ? 'simple' : 'advanced')
}

/**
 * 参数状态用驼峰，与 i18n 映射表的 key 一致；发请求时再转成后端的下划线。
 *
 * 初值先空着，等配置或档位列表到了再填：档位列表是异步来的，在这里写死一个
 * 内置档位等于把后端那份定义又抄了一遍。
 */
const params = ref<Record<string, number | boolean>>({})
const presetId = ref(CUSTOM_PRESET)
const source = ref<ScanSource>({ official: true, remote: [], customText: '' })
/** 官方网段条数。后端还没提供这个数字时显示 0，不假装知道。 */
const officialCount = ref(0)

const running = computed(() => task.running)
const showEmpty = computed(() => !running.value && results.total === 0)

/**
 * 把「开始扫描」注册给动作注册表。
 *
 * 命令面板与 Ctrl+Enter 都从这里调用，走的是与按钮完全相同的路径——参数、
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

/**
 * 上一次与服务端对齐过的参数。
 *
 * 用它做差分，而不是每次把整组参数写回去：整组写会把用户没碰过的项也标成
 * `user`，自适应从此再也不能动它们——而用户只是改了其中一个。
 */
const synced = ref<Record<string, number | boolean>>({})

let persistParamsTimer: ReturnType<typeof setTimeout> | null = null

/** 把用户改过的参数写回配置，带防抖。 */
function schedulePersistParams(): void {
  if (persistParamsTimer !== null) clearTimeout(persistParamsTimer)
  persistParamsTimer = setTimeout(flushParams, 400)
}

function flushParams(): void {
  persistParamsTimer = null
  const { patch, origins } = diffScanParams(params.value, synced.value)
  if (Object.keys(patch).length === 0) return
  sendCommand(CMD.settingsUpdate, { patch: { scan: patch }, origins })
}

watch(() => params.value, schedulePersistParams, { deep: true })

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
    synced.value = filled
    params.value = { ...params.value, ...filled }
  },
)

/**
 * 按配置与档位列表初始化面板。
 *
 * 档位列表是异步来的，所以这里既要能在挂载时跑，也要在列表到达后再跑一次——
 * 否则首次进入会一直显示「自定义」，只因为档位还没到。
 *
 * 返回是否已经定下参数：定不下来（档位还没到）时留给下一次调用，而不是拿一份
 * 空参数当结果。
 */
function syncFromSettings(): boolean {
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
    // 首次运行：从启动档位开始，并把它真的写进配置。
    //
    // 只填界面不写配置是不够的：配置里那份参数来源表是自适应逻辑的唯一依据，
    // 不写的话自适应会把档位填的值当成「用户从未碰过的默认值」，一识别到移动
    // 宽带就把并发降下去——而那正是用户刚选的档位。
    const preset = presets.byID(presets.defaultID)
    if (!preset) return false
    const values = presetValues(preset)
    params.value = { ...params.value, ...values }
    // 这次填值不算用户改动，因此先把对齐基线推上去，别被差分当成手改写回去。
    synced.value = values
    presetId.value = preset.id
    presets.use(preset.id)
    return true
  }

  const filled = scanParamsFromConfig(scan)
  if (Object.keys(filled).length === 0) return false
  synced.value = filled
  params.value = { ...params.value, ...filled }
  presetId.value = matchPreset(params.value, presets.list)
  return true
}

/**
 * 套用历史页送来的参数快照。
 *
 * 只覆盖快照里带的项，来源文本单独填——官方网段与远端地址取自当前配置：
 * 历史里记的是「当时扫了什么」，而不是「现在该怎么配来源」。
 */
function applyRerun(): void {
  const request = rerun.take()
  if (!request) return
  params.value = { ...params.value, ...request.params }
  if (request.customText !== '') source.value = { ...source.value, customText: request.customText }
  presetId.value = request.preset !== '' ? request.preset : CUSTOM_PRESET
}

onMounted(() => {
  const saved = loadSnapshot()
  if (saved && worthRestoring(saved)) session.value = saved
  unregister = actions.register('startScan', start)
  syncFromSettings()
  applyRerun()
})

// 页面可能一直挂着（比如从命令面板跳回来），因此不能只在挂载时取一次。
watch(() => rerun.pending, applyRerun)

// 档位列表到达后补一次初始化。已经定下参数时只补判定档位，不重填参数——用户
// 可能已经在改了，把参数覆盖回去比显示「自定义」更糟。
watch(() => presets.loaded, () => {
  if (!presets.loaded) return
  if (Object.keys(params.value).length === 0) syncFromSettings()
  else presetId.value = matchPreset(params.value, presets.list)
})

onBeforeUnmount(() => {
  unregister?.()
  // 离开页面时把还没落盘的改动补写一次：防抖窗口里离开的话，那点改动就丢了，
  // 而用户会以为它已经保存。
  if (persistTimer !== null) clearTimeout(persistTimer)
  if (persistParamsTimer !== null) clearTimeout(persistParamsTimer)
  flushParams()
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
  // 档位标识只用于历史归档，不进参数快照。手调的参数不属于任何档位，留空——
  // 硬塞一个「自定义」进去，历史里就会出现一个并不存在的档位名。
  payload.preset = presetId.value === CUSTOM_PRESET ? '' : presetId.value
  return payload
}

function start(): void {
  if (!sendCommand(CMD.scanStart, buildRequest())) return
  log.push(t('scan.start'))
}

function stop(): void {
  sendCommand(CMD.scanStop)
  log.push(t('scan.stop'), 'warn')
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
      v-if="migrate.shouldOffer"
      tone="info"
      :message="migrateOffer"
      :action-label="migrate.running ? t('migrate.running') : t('migrate.import')"
      @action="migrate.run()"
      @close="migrate.dismiss()"
    />
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
      @close="migrate.dismiss()"
    />

    <!--
      首屏只有「选档位 + 开始扫描」：这两件事是同一个决定的两半。数据源、参数
      明细、过程、日志全部收进「详细设置」，默认不展开——首次进来的用户不需要
      先读懂六个面板才敢点按钮。
    -->
    <div class="actions">
      <button v-if="!running" type="button" class="ct-btn ct-btn--primary ct-btn--lg" @click="start">
        {{ t('scan.start') }}
      </button>
      <button v-else type="button" class="ct-btn ct-btn--lg" @click="stop">
        {{ t('scan.stop') }}
      </button>
      <PresetPicker v-model:params="params" v-model:preset-id="presetId" />
      <!--
        「智能推荐」是显式操作：点了才生效，且不受来源限制（可以改用户手填过的
        值）。它是自动自适应的手动对应物——那条自动路径只在 default/preset 来源
        上动手，手填过的值永远只给建议。
      -->
      <button
        type="button"
        class="ct-link"
        :disabled="adaptive.recommending"
        :title="t('adaptive.recommendHint')"
        @click="adaptive.recommend()"
      >
        {{ adaptive.recommending ? t('adaptive.recommending') : t('adaptive.recommend') }}
      </button>
      <span class="spacer" />
      <button type="button" class="ct-link" :aria-expanded="ui.showAdvanced" @click="toggleDetails">
        {{ ui.showAdvanced ? t('preset.collapse') : t('scan.details') }}
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
