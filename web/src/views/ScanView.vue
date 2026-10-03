<script setup lang="ts">
/**
 * 扫描页：让用户在不理解任何参数的前提下，一次点击得到结果。
 *
 * 这个文件只做编排：连 store、组装请求、把区域分给组件。具体控件一律在
 * 子组件里——页面模板保持短，才能逼着结构被拆开。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { sendCommand } from '@/api/client'
import LogPanel from '@/components/scan/LogPanel.vue'
import PresetPanel from '@/components/scan/PresetPanel.vue'
import ProcessPanel from '@/components/scan/ProcessPanel.vue'
import SourcePanel, { type ScanSource } from '@/components/scan/SourcePanel.vue'
import Banner from '@/components/ui/Banner.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import { t } from '@/i18n'
import { CUSTOM_PRESET, matchPreset, presetValues } from '@/i18n/params'
import { useActionStore } from '@/stores/actions'
import { useGeoStore } from '@/stores/geo'
import { useLogStore } from '@/stores/log'
import { usePresetsStore } from '@/stores/presets'
import { useResultsStore } from '@/stores/results'
import { useSettingsStore } from '@/stores/settings'
import { useTaskStore } from '@/stores/task'

const task = useTaskStore()
const geo = useGeoStore()
const log = useLogStore()
const results = useResultsStore()
const settings = useSettingsStore()
const actions = useActionStore()
const presets = usePresetsStore()

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

/** 配置里的 scan 组是下划线命名，映射成界面用的驼峰。 */
const WIRE_KEYS: Record<string, string> = {
  sampleMax: 'sample_max',
  workers: 'workers',
  latencyThreshold: 'latency_threshold',
  pingTimes: 'ping_times',
  port: 'port',
  timeoutMs: 'timeout_ms',
  retry: 'retry',
  twoPhase: 'two_phase',
  verifyNodes: 'verify_nodes',
}

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
    sendCommand('settings/update', {
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
 * 按配置与档位列表初始化面板。
 *
 * 参数优先取配置：那是用户上次用过的那组值。配置里一个扫描参数都没有时（首次
 * 运行）退到启动档位，界面不会空着。
 *
 * 档位列表是异步来的，所以这里既要能在挂载时跑，也要在列表到达后再跑一次——
 * 否则首次进入会一直显示「自定义」，只因为档位还没到。
 */
function syncFromSettings(): void {
  const scan = settings.values?.scan as Record<string, unknown> | undefined
  const next: Record<string, number | boolean> = {}
  for (const [camel, wire] of Object.entries(WIRE_KEYS)) {
    const value = scan?.[wire]
    if (typeof value === 'number' || typeof value === 'boolean') next[camel] = value
  }

  const filled = Object.keys(next).length > 0 ? next : presetValues(presets.byID(presets.defaultID))
  if (Object.keys(filled).length === 0) return

  params.value = { ...params.value, ...filled }
  presetId.value = matchPreset(params.value, presets.list)

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
}

onMounted(() => {
  unregister = actions.register('startScan', start)
  syncFromSettings()
})

// 档位列表到达后重新判定一次档位。只在这里补判定，不重填参数——用户可能已经
// 在改了，把参数覆盖回去比显示「自定义」更糟。
watch(() => presets.loaded, () => {
  if (!presets.loaded) return
  if (Object.keys(params.value).length === 0) syncFromSettings()
  else presetId.value = matchPreset(params.value, presets.list)
})

onBeforeUnmount(() => {
  unregister?.()
  if (persistTimer !== null) clearTimeout(persistTimer)
})

/** 组装后端要的扫描参数。 */
function buildRequest(): Record<string, unknown> {
  const payload: Record<string, unknown> = { ip_version: 4 }
  for (const [camel, wire] of Object.entries(WIRE_KEYS)) {
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
  if (!sendCommand('scan/start', buildRequest())) return
  log.push(t('scan.start'))
}

function stop(): void {
  sendCommand('scan/stop')
  log.push(t('scan.stop'), 'warn')
}
</script>

<template>
  <div class="page">
    <Banner v-if="geo.warning" tone="warn" :message="geo.warning.message" @close="geo.dismissWarning()" />

    <div class="columns">
      <div class="main">
        <SourcePanel v-model="source" :official-count="officialCount" />
        <PresetPanel v-model:params="params" v-model:preset-id="presetId" :disabled="running" />

        <div class="actions">
          <button v-if="!running" type="button" class="ct-btn ct-btn--primary ct-btn--lg" @click="start">
            {{ t('scan.start') }}
          </button>
          <button v-else type="button" class="ct-btn ct-btn--lg" @click="stop">
            {{ t('scan.stop') }}
          </button>
          <span class="ct-subtle">Ctrl + Enter</span>
        </div>
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
  align-items: center;
  gap: var(--space-3);
}

@media (max-width: 1024px) {
  .columns {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
