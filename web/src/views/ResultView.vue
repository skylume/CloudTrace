<script setup lang="ts">
/**
 * 结果页：让用户 3 秒内看懂结果质量，1 次点击取走想要的 IP。
 *
 * 只做编排。表格、详情、统计都在子组件里——页面模板短，结构才会被逼着拆开。
 */
import { computed, onMounted, ref } from 'vue'

import { sendCommand } from '@/api/client'
import DataTable from '@/components/result/DataTable.vue'
import RecordCardList from '@/components/result/RecordCardList.vue'
import RadarPulse from '@/components/ui/RadarPulse.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import { t } from '@/i18n'
import { COLUMN_PRESETS, useFieldsStore, type ColumnPresetId } from '@/stores/fields'
import { useResultsStore } from '@/stores/results'
import { useTaskStore } from '@/stores/task'
import { useUIStore } from '@/stores/ui'
import { formatLatency, formatSpeed } from '@/utils/latency'
import { useNarrow } from '@/utils/useMediaQuery'

const results = useResultsStore()
const fields = useFieldsStore()
const task = useTaskStore()
const ui = useUIStore()

const narrow = useNarrow()
const view = ref<'result' | 'speed'>('result')
const expanded = ref<string[]>([])

onMounted(() => void fields.load())

const presetSegments = computed(() =>
  COLUMN_PRESETS.map((preset) => ({ value: preset.id, label: t(preset.labelKey as never) })),
)

/** 统计卡：结果视图看延迟，测速视图看速度——同一批数据，两个关注点。 */
const stats = computed(() => {
  const summary = results.stats
  if (view.value === 'speed') {
    return [
      { label: t('result.stat.bestSpeed'), value: formatSpeed(summary.best_speed), unit: 'MB/s', tone: '' },
      { label: t('result.stat.avgSpeed'), value: formatSpeed(summary.avg_speed), unit: 'MB/s', tone: '' },
      { label: t('result.stat.qualified'), value: String(summary.qualified), unit: '', tone: '' },
      { label: t('result.stat.total'), value: String(summary.total), unit: '', tone: '' },
    ]
  }
  return [
    { label: t('result.stat.usable'), value: String(summary.total), unit: '', tone: '' },
    { label: t('result.stat.regions'), value: String(Object.keys(summary.region_dist).length), unit: '', tone: '' },
    { label: t('result.stat.minLatency'), value: formatLatency(summary.min_latency), unit: 'ms', tone: 'ok' },
    { label: t('result.stat.avgLatency'), value: formatLatency(summary.avg_latency), unit: 'ms', tone: '' },
  ]
})

const showEmpty = computed(() => results.total === 0 && !task.running)
const selectedCount = computed(() => results.selected.size)

/** 复制前几条：按当前排序取，与用户看到的顺序一致。 */
async function copyTop(count: number): Promise<void> {
  const text = results.visible
    .slice(0, count)
    .map((record) => `${record.ip}:${record.port}`)
    .join('\n')
  if (text === '') return
  try {
    await navigator.clipboard.writeText(text)
    ui.pushToast({ kind: 'ok', message: t('common.copied') })
  } catch {
    // 剪贴板在非安全上下文里不可用；此时提示用户手动选，不要静默失败。
    ui.pushToast({ kind: 'warn', message: t('common.copy') })
  }
}

function speedSelected(): void {
  const targets = results.selectedRecords
  if (targets.length === 0) return
  startSpeed(targets)
}

/** 「测速本组」走同一条路径：先选中，再按选中发起。 */
function onGroupSpeed(records: typeof results.all): void {
  startSpeed(records)
}

function startSpeed(targets: typeof results.all): void {
  if (targets.length === 0) return
  sendCommand('speed/start', { scope: 'single', targets })
  view.value = 'speed'
}

function applyPreset(id: string): void {
  fields.applyPreset(id as ColumnPresetId)
}
</script>

<template>
  <div class="page">
    <div v-if="showEmpty" class="ct-card empty">
      <RadarPulse />
      <p class="empty-title">{{ t('result.empty') }}</p>
      <button type="button" class="ct-btn ct-btn--primary" @click="ui.activeView = 'scan'">
        {{ t('result.emptyAction') }}
      </button>
    </div>

    <template v-else>
      <div class="stats">
        <div v-for="item in stats" :key="item.label" class="stat">
          <span class="ct-subtle">{{ item.label }}</span>
          <span class="stat-value tnum" :class="item.tone">{{ item.value }}<small>{{ item.unit }}</small></span>
        </div>
      </div>

      <div class="toolbar">
        <SegmentedControl
          v-model="view"
          :segments="[
            { value: 'result', label: t('result.view.result') },
            { value: 'speed', label: t('result.view.speed') },
          ]"
        />
        <input
          id="ct-result-search"
          v-model="results.keyword"
          class="ct-input search"
          type="search"
          :placeholder="t('common.search')"
        />
        <span class="spacer" />
        <SegmentedControl :segments="presetSegments" :model-value="fields.presetId" @update:model-value="applyPreset" />
        <select v-model="results.groupBy" class="ct-input" :aria-label="t('result.group.none')">
          <option value="none">{{ t('result.group.none') }}</option>
          <option value="colo">{{ t('result.group.colo') }}</option>
          <option value="region">{{ t('result.group.region') }}</option>
          <option value="asn">{{ t('result.group.asn') }}</option>
        </select>
        <button type="button" class="ct-btn" @click="copyTop(3)">{{ t('result.copyTop') }}</button>
        <button type="button" class="ct-btn" @click="copyTop(results.visible.length)">{{ t('result.copyAll') }}</button>
        <button type="button" class="ct-btn ct-btn--primary" :disabled="selectedCount === 0" @click="speedSelected">
          {{ t('result.speedSelected', { count: selectedCount }) }}
        </button>
      </div>

      <RecordCardList v-if="narrow" :records="results.visible" />
      <DataTable v-else v-model:expanded="expanded" :columns="fields.columns" :records="results.visible" @speed="onGroupSpeed" />
      <p class="ct-subtle foot">{{ results.visible.length }} / {{ results.total }}</p>
    </template>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.stats {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: var(--space-3);
}

.stat {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border);
  border-top-color: var(--color-border-highlight);
  border-radius: var(--radius-lg);
  background: var(--color-surface);
  box-shadow: var(--elevation-1);
}

.stat-value {
  font-size: var(--font-size-2xl);
  font-weight: 500;
  line-height: 1.2;
}

.stat-value small {
  margin-left: 2px;
  color: var(--color-text-subtle);
  font-size: var(--font-size-sm);
  font-weight: 400;
}

.stat-value.ok {
  color: var(--color-ok);
}

.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.spacer {
  flex: 1;
}

.toolbar select {
  width: auto;
}

.search {
  width: 180px;
}

.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-7) var(--space-4);
  text-align: center;
}

.empty-title {
  font-weight: 500;
}

.foot {
  text-align: right;
}
</style>
