<script setup lang="ts">
/**
 * 扫描页：让用户在不理解任何参数的前提下，一次点击得到结果。
 *
 * 这个文件只做编排：连 store、组装请求、把区域分给组件。具体控件一律在
 * 子组件里——页面模板保持短，才能逼着结构被拆开。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import { sendCommand } from '@/api/client'
import LogPanel from '@/components/scan/LogPanel.vue'
import PresetPanel from '@/components/scan/PresetPanel.vue'
import ProcessPanel from '@/components/scan/ProcessPanel.vue'
import SourcePanel, { type ScanSource } from '@/components/scan/SourcePanel.vue'
import Banner from '@/components/ui/Banner.vue'
import RadarPulse from '@/components/ui/RadarPulse.vue'
import { t } from '@/i18n'
import { matchPreset, presetValues } from '@/i18n/params'
import { useActionStore } from '@/stores/actions'
import { useGeoStore } from '@/stores/geo'
import { useLogStore } from '@/stores/log'
import { useResultsStore } from '@/stores/results'
import { useSettingsStore } from '@/stores/settings'
import { useTaskStore } from '@/stores/task'

const task = useTaskStore()
const geo = useGeoStore()
const log = useLogStore()
const results = useResultsStore()
const settings = useSettingsStore()
const actions = useActionStore()

/** 参数状态用驼峰，与 i18n 映射表的 key 一致；发请求时再转成后端的下划线。 */
const params = ref<Record<string, number | boolean>>({ ...presetValues('fast') })
const presetId = ref('fast')
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

onMounted(() => {
  unregister = actions.register('startScan', start)
  // 配置到了就按配置初始化；没到时先用「快速」档，界面不会空着。
  const scan = settings.values?.scan as Record<string, unknown> | undefined
  if (!scan) return
  const next = { ...params.value }
  for (const [camel, wire] of Object.entries(WIRE_KEYS)) {
    const value = scan[wire]
    if (typeof value === 'number' || typeof value === 'boolean') next[camel] = value
  }
  params.value = next
  presetId.value = matchPreset(next)
})

onBeforeUnmount(() => unregister?.())

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
        <div v-if="showEmpty" class="ct-card empty">
          <RadarPulse />
          <p class="empty-title">{{ t('scan.empty.title') }}</p>
          <p class="ct-subtle">{{ t('scan.empty.desc') }}</p>
        </div>
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

.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-4);
  text-align: center;
}

.empty-title {
  font-weight: 500;
}

@media (max-width: 1024px) {
  .columns {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
