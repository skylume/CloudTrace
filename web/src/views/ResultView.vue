<script setup lang="ts">
/**
 * 结果页：让用户 3 秒内看懂结果质量，1 次点击取走想要的 IP。
 *
 * 只做编排。表格、详情、统计都在子组件里——页面模板短，结构才会被逼着拆开。
 */
import { computed, onMounted, ref } from 'vue'

import { sendCommand } from '@/api/client'
import DataTable from '@/components/result/DataTable.vue'
import MobileActionBar from '@/components/result/MobileActionBar.vue'
import RecordCardList from '@/components/result/RecordCardList.vue'
import Banner from '@/components/ui/Banner.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Pager from '@/components/ui/Pager.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import { t } from '@/i18n'
import type { IPRecord } from '@/api/types'
import { COLUMN_PRESETS, useFieldsStore, type ColumnPresetId } from '@/stores/fields'
import { useExportStore } from '@/stores/export'
import { useHistoryStore } from '@/stores/history'
import { useResultsStore } from '@/stores/results'
import { useSettingsStore } from '@/stores/settings'
import { useSpeedStore } from '@/stores/speed'
import { useTaskStore } from '@/stores/task'
import { useUIStore } from '@/stores/ui'
import { formatLatency, formatSpeed } from '@/utils/latency'
import { buildSpeedParams } from '@/utils/speedParams'
import { useNarrow } from '@/utils/useMediaQuery'

const results = useResultsStore()
const fields = useFieldsStore()
const task = useTaskStore()
const speed = useSpeedStore()
const settings = useSettingsStore()
const exporter = useExportStore()
const history = useHistoryStore()

/**
 * 下拉刷新。
 *
 * 「刷新」在这里有明确语义，不是走个过场：
 *   - 结果来自某份历史 -> 重新读那一份（标签与备注可能被别的窗口改过）
 *   - 结果是本次扫描出来的 -> 从服务端拉「最新一份」，把另一个窗口或上一次
 *     运行的结果取回来
 *
 * 只在页面滚到顶部时才触发：列表已经往下翻过时再拉，用户想滚回顶部，而不是刷新。
 */
const pullDistance = ref(0)
const PULL_THRESHOLD = 70
let pullStartY = 0
let pulling = false

function onPullStart(event: TouchEvent): void {
  if (window.scrollY > 0) return
  pullStartY = event.touches[0]?.clientY ?? 0
  pulling = true
}

function onPullMove(event: TouchEvent): void {
  if (!pulling) return
  const current = event.touches[0]?.clientY ?? 0
  // 只认下拉；上滑是正常滚动，跟着算会让页面卡住。
  pullDistance.value = Math.max(0, current - pullStartY)
}

async function onPullEnd(): Promise<void> {
  if (!pulling) return
  pulling = false
  const distance = pullDistance.value
  pullDistance.value = 0
  if (distance < PULL_THRESHOLD) return
  await refreshResults()
}

async function refreshResults(): Promise<void> {
  if (results.sourceId !== '') {
    history.load(results.sourceId, { count: results.total })
    return
  }

  try {
    const response = await fetch('/latest.json', { credentials: 'same-origin' })
    if (!response.ok) {
      // 没跑过任务时是 404，这不是故障，只是没有可刷新的东西。
      ui.pushToast({ kind: 'warn', message: t('result.empty') })
      return
    }
    results.replaceAll((await response.json()) as IPRecord[])
    ui.pushToast({ kind: 'ok', message: t('result.refreshed') })
  } catch {
    ui.pushToast({ kind: 'bad', message: t('error.E_NETWORK') })
  }
}
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

/**
 * 发起测速。
 *
 * 参数在这里按当前配置组装好整份发出去。后端拿到的是**完整参数**，不会
 * 替我们去读配置——只发目标和范围的话，设置页里改过的并发、间隔、测速源
 * 就一个都不会生效。
 */
function startSpeed(targets: typeof results.all): void {
  if (targets.length === 0) return
  sendCommand('speed/start', buildSpeedParams(settings.values, { scope: 'single', targets }))
  view.value = 'speed'
}

function applyPreset(id: string): void {
  fields.applyPreset(id as ColumnPresetId)
}

/**
 * 导出当前结果。
 *
 * 字段用当前可见列：用户看到的和导出的必须是同一份东西，否则「导出少了几列」
 * 会被当成丢数据。格式留空表示用后端的默认值。
 */
function exportResult(): void {
  if (results.visible.length === 0) return
  // 带上来历：显示的是历史加载的那一份时，导「最新」会导出完全不同的数据。
  exporter.request({ fields: fields.visibleKeys, id: results.sourceId })
}

/**
 * 按熔断建议改配置。
 *
 * 走 settings/update 而不是只改本地：并发与测速源都是服务端配置，只改本地
 * 的话下一次测速仍然会用回原值——用户会以为建议没生效。
 */
/**
 * 选源说明的文案。
 *
 * 认不出的原因码就退回显示原值：后端加了新原因而前端还没跟上时，至少不会显示
 * 空白——空白会让人以为这个功能坏了。
 */
function sourceReason(code: string): string {
  const key = `speed.source.${code}`
  const translated = t(key as never)
  return translated === key ? code : translated
}

function applyBreakerFix(patch: Record<string, unknown>): void {
  sendCommand('settings/update', { patch: { speed: patch }, origins: {} })
  speed.dismissBreaker()
}
</script>

<template>
  <div
    class="page"
    @touchstart.passive="narrow && onPullStart($event)"
    @touchmove.passive="narrow && onPullMove($event)"
    @touchend="narrow && onPullEnd()"
  >
    <p v-if="pullDistance > 0" class="pull ct-subtle" :style="{ height: Math.min(pullDistance, 90) + 'px' }">
      {{ pullDistance >= PULL_THRESHOLD ? t('common.retry') : '' }}
    </p>

    <EmptyState
      v-if="showEmpty"
      class="ct-card"
      :title="t('result.empty')"
      :desc="t('result.emptyDesc')"
      :action-label="t('result.emptyAction')"
      @action="ui.activeView = 'scan'"
    />

    <template v-else>
      <Banner
        v-if="speed.breaker && view === 'speed'"
        tone="warn"
        :message="speed.breaker.message"
        :action-label="t('speed.lowerConcurrency', { value: speed.suggestedConcurrency() })"
        @action="applyBreakerFix({ concurrency: speed.suggestedConcurrency() })"
        @close="speed.dismissBreaker()"
      />

      <p v-if="view === 'speed' && speed.source" class="ct-subtle source-line">
        {{ t('speed.source.title') }}：<span class="ct-mono">{{ speed.source.url }}</span>
        · {{ sourceReason(speed.source.code) }}
        <template v-if="speed.source.detail">（{{ speed.source.detail }}）</template>
      </p>

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
        <button type="button" class="ct-btn" :disabled="exporter.pending" @click="exportResult">
          {{ t('common.export') }}
        </button>
        <button type="button" class="ct-btn ct-btn--primary" :disabled="selectedCount === 0" @click="speedSelected">
          {{ t('result.speedSelected', { count: selectedCount }) }}
        </button>
      </div>

      <RecordCardList v-if="narrow" :records="results.paged" />
      <DataTable v-else v-model:expanded="expanded" :columns="fields.columns" :records="results.paged" @speed="onGroupSpeed" />
      <p class="ct-subtle foot">{{ results.visible.length }} / {{ results.total }}</p>
      <!--
        只有一页时不显示分页控件：一个「第 1 / 1 页」的工具栏只是占地方，
        而绝大多数扫描的结果本来就只有一页。
      -->
      <Pager
        v-if="results.pageCount > 1"
        :page="results.currentPage"
        :page-count="results.pageCount"
        :total="results.visible.length"
        :page-size="ui.pageSize"
        @update:page="results.setPage"
      />

      <MobileActionBar
        v-if="narrow"
        :selected-count="selectedCount"
        :in-speed-view="view === 'speed'"
        @copy="copyTop(results.visible.length)"
        @speed="speedSelected"
        @export="exportResult"
      />
    </template>
  </div>
</template>

<style scoped>
.pull {
  display: flex;
  align-items: center;
  justify-content: center;
  margin: 0;
  overflow: hidden;
  transition: height var(--duration-fast) var(--ease);
}

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

.source-line {
  margin: 0;
  overflow-wrap: anywhere;
}

.foot {
  text-align: right;
}
</style>
