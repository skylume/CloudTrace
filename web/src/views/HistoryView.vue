<script setup lang="ts">
/**
 * 历史页：让结果成为资产而不是一次性消耗品。
 *
 * 这是差异化的核心，所以做得比别处厚：列表能筛、能收藏、能打标签，点开能看到
 * 那一份的参数快照与结果，两份之间还能对比出线路变化。
 *
 * 筛选全在本地做：索引里已经放够了要用的字段（时间、标签、结果数），再往服务端
 * 跑一趟只会让每次输入都慢半拍。
 */
import { computed, onMounted, ref, watch } from 'vue'

import HistoryDrawer from '@/components/history/HistoryDrawer.vue'
import HistoryList from '@/components/history/HistoryList.vue'
import Banner from '@/components/ui/Banner.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import { t } from '@/i18n'
import { PARAM_WIRE_KEYS } from '@/i18n/params'
import type { HistoryRecord } from '@/api/types'
import { useHistoryStore } from '@/stores/history'
import { useRerunStore } from '@/stores/rerun'
import { useResultsStore } from '@/stores/results'
import { useUIStore } from '@/stores/ui'
import { formatLatency } from '@/utils/latency'

const history = useHistoryStore()
const results = useResultsStore()
const ui = useUIStore()
const rerunStore = useRerunStore()

const keyword = ref('')
const starredOnly = ref(false)
const selectedTags = ref<string[]>([])
const minCount = ref('')
const timeRange = ref<'all' | 'today' | 'week' | 'month'>('all')
const compareIds = ref<string[]>([])
/** 加载后的提示：这份历史用的是别的参数。 */
const paramNotice = ref('')

onMounted(() => history.refresh())

const TIME_RANGES = ['all', 'today', 'week', 'month'] as const

/** 时间下限。0 表示不限。 */
function cutoffOf(range: (typeof TIME_RANGES)[number]): number {
  const DAY = 86_400_000
  if (range === 'today') {
    const midnight = new Date()
    midnight.setHours(0, 0, 0, 0)
    return midnight.getTime()
  }
  if (range === 'week') return Date.now() - 7 * DAY
  if (range === 'month') return Date.now() - 30 * DAY
  return 0
}

const filtered = computed(() => {
  const text = keyword.value.trim().toLowerCase()
  const parsedMin = Number.parseInt(minCount.value, 10)
  const min = Number.isFinite(parsedMin) && parsedMin > 0 ? parsedMin : 0
  const cutoff = cutoffOf(timeRange.value)

  return history.entries.filter((entry) => {
    if (starredOnly.value && !entry.starred) return false
    if (min > 0 && entry.count < min) return false
    if (cutoff > 0) {
      const at = new Date(entry.created_at).getTime()
      if (Number.isFinite(at) && at < cutoff) return false
    }
    if (selectedTags.value.length > 0) {
      const tags = entry.tags ?? []
      // 多选按「都得有」算：选了「公司网络 + 香港优选」的人要的是交集。
      if (!selectedTags.value.every((tag) => tags.includes(tag))) return false
    }
    if (text === '') return true
    const haystack = [entry.preset, entry.note, ...(entry.tags ?? []), ...(entry.regions ?? [])]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
    return haystack.includes(text)
  })
})

const diff = computed(() => history.diff)
const activeFilterCount = computed(
  () => selectedTags.value.length + (minCount.value !== '' ? 1 : 0) + (timeRange.value === 'all' ? 0 : 1),
)

function toggleTag(tag: string): void {
  selectedTags.value = selectedTags.value.includes(tag)
    ? selectedTags.value.filter((item) => item !== tag)
    : [...selectedTags.value, tag]
}

function clearFilters(): void {
  selectedTags.value = []
  minCount.value = ''
  timeRange.value = 'all'
}

/**
 * 加载一份历史。
 *
 * 带上当前参数快照：服务端据此判断「这份历史是用别的参数跑的」，用户才不会
 * 拿着两份不可比的结果去做对比。
 */
function load(id: string): void {
  history.load(id, { count: results.total })
}

/**
 * 记录到达就送进结果集，并跳到结果页。
 *
 * 不能只把记录放进 `history.loaded` 然后等用户再点一次横幅：横幅只在参数有
 * 差异时才出现，参数一致时「加载」点了什么也不会发生——用户只会以为功能坏了。
 */
watch(
  () => history.loaded,
  (payload) => {
    if (!payload) return
    results.replaceAll(payload.record.results, payload.record.id)
    paramNotice.value =
      payload.paramDiff.length > 0
        ? t('history.paramDiff', { minutes: Math.round(payload.ageMinutes) })
        : ''
    ui.activeView = 'result'
  },
)

function toggleCompare(id: string): void {
  compareIds.value = compareIds.value.includes(id)
    ? compareIds.value.filter((item) => item !== id)
    : [...compareIds.value, id].slice(-2)
  if (compareIds.value.length === 2) {
    history.compare(compareIds.value[0]!, compareIds.value[1]!)
  }
}

function loadFromDrawer(id: string): void {
  history.closeDetail()
  load(id)
}

/** 参数快照里的键是后端的下划线名，面板用的是驼峰名。 */
const CAMEL_BY_WIRE = new Map(
  Object.entries(PARAM_WIRE_KEYS).map(([camel, wire]) => [wire, camel] as const),
)

function paramsFromSnapshot(raw: unknown): Record<string, number | boolean> {
  if (!raw || typeof raw !== 'object') return {}
  const out: Record<string, number | boolean> = {}
  for (const [key, value] of Object.entries(raw as Record<string, unknown>)) {
    const camel = CAMEL_BY_WIRE.get(key)
    if (!camel) continue
    if (typeof value === 'number' || typeof value === 'boolean') out[camel] = value
  }
  return out
}

/** 以此参数重跑：把快照交给扫描页，然后切过去。 */
function rerun(record: HistoryRecord): void {
  const snapshot = (record.params ?? {}) as Record<string, unknown>
  rerunStore.request({
    params: paramsFromSnapshot(record.params),
    customText: typeof snapshot.custom_source === 'string' ? snapshot.custom_source : '',
    preset: record.preset ?? '',
  })
  history.closeDetail()
  ui.activeView = 'scan'
}

function closeDiff(): void {
  history.diff = null
  compareIds.value = []
}
</script>

<template>
  <div class="page">
    <Banner v-if="paramNotice" tone="warn" :message="paramNotice" />

    <div class="bar">
      <input v-model="keyword" class="ct-input grow" :placeholder="t('common.search')" />
      <label class="toggle">
        <input v-model="starredOnly" type="checkbox" class="ct-check" />
        <span>{{ t('history.starred') }}</span>
      </label>
      <label class="toggle">
        <span class="ct-subtle">{{ t('history.filter.minCount') }}</span>
        <input v-model="minCount" class="ct-input tiny" type="number" min="0" inputmode="numeric" />
      </label>
      <select v-model="timeRange" class="ct-input" :aria-label="t('history.filter.time')">
        <option v-for="range in TIME_RANGES" :key="range" :value="range">
          {{ t(`history.filter.time${range === 'all' ? 'All' : range === 'today' ? 'Today' : range === 'week' ? 'Week' : 'Month'}` as never) }}
        </option>
      </select>
      <span class="spacer" />
      <button v-if="activeFilterCount > 0" type="button" class="ct-link" @click="clearFilters">
        {{ t('result.filter.clear') }}
      </button>
      <span class="ct-subtle tnum">{{ filtered.length }} / {{ history.total }}</span>
      <button type="button" class="ct-btn" @click="history.refresh()">{{ t('common.retry') }}</button>
    </div>

    <div v-if="history.allTags.length > 0" class="tags">
      <span class="ct-subtle">{{ t('history.filter.tags') }}</span>
      <button
        v-for="tag in history.allTags"
        :key="tag"
        type="button"
        class="chip"
        :class="{ on: selectedTags.includes(tag) }"
        @click="toggleTag(tag)"
      >
        {{ tag }}
      </button>
    </div>

    <div v-if="diff" class="ct-card diff">
      <div class="diff-item">
        <span class="ct-subtle">{{ t('result.stat.avgLatency') }}</span>
        <b class="tnum" :class="diff.latency_delta <= 0 ? 'better' : 'worse'">
          {{ diff.latency_delta > 0 ? '↑' : '↓' }} {{ formatLatency(Math.abs(diff.latency_delta)) }}ms
        </b>
      </div>
      <div class="diff-item"><span class="ct-subtle">{{ t('history.diff.added') }}</span><b class="tnum better">{{ diff.added_count }}</b></div>
      <div class="diff-item"><span class="ct-subtle">{{ t('history.diff.removed') }}</span><b class="tnum worse">{{ diff.removed_count }}</b></div>
      <div class="diff-item"><span class="ct-subtle">{{ t('history.diff.changed') }}</span><b class="tnum">{{ diff.changed_count }}</b></div>
      <span class="spacer" />
      <button type="button" class="ct-link" @click="closeDiff">{{ t('common.close') }}</button>
    </div>

    <EmptyState
      v-if="filtered.length === 0"
      class="ct-card"
      :title="t('history.empty')"
      :desc="t('history.emptyDesc')"
      :action-label="t('result.emptyAction')"
      @action="ui.activeView = 'scan'"
    />
    <HistoryList
      v-else
      :entries="filtered"
      :compare-ids="compareIds"
      @load="load"
      @open="history.get"
      @toggle-compare="toggleCompare"
    />

    <HistoryDrawer
      v-if="history.detail"
      :record="history.detail"
      :time-format="ui.timeFormat"
      @close="history.closeDetail()"
      @load="loadFromDrawer"
      @rerun="rerun"
    />
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.grow {
  flex: 1 1 200px;
}

.tiny {
  width: 72px;
}

.toggle {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-text-muted);
  font-size: var(--font-size-sm);
  cursor: pointer;
}

.tags {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1);
  font-size: var(--font-size-sm);
}

.chip {
  padding: 2px var(--space-3);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-pill);
  background: var(--color-surface);
  color: var(--color-text-muted);
  font-size: var(--font-size-xs);
  cursor: pointer;
}

.chip.on {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
  color: var(--color-primary-text);
}

.spacer {
  flex: 1;
}

.diff {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-5);
}

.diff-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.better {
  color: var(--color-ok);
}

.worse {
  color: var(--color-bad);
}
</style>
