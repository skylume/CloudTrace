<script setup lang="ts">
/**
 * 历史页：让结果成为资产而不是一次性消耗品。
 *
 * 这是差异化的核心，所以做得比别处厚：列表能筛、能收藏、能打标签，加载时
 * 会告诉你「这份是用别的参数跑的」，两份之间还能对比出线路变化。
 */
import { computed, onMounted, ref } from 'vue'

import HistoryList from '@/components/history/HistoryList.vue'
import Banner from '@/components/ui/Banner.vue'
import { t } from '@/i18n'
import { useHistoryStore } from '@/stores/history'
import { useResultsStore } from '@/stores/results'
import { useUIStore } from '@/stores/ui'
import { formatLatency } from '@/utils/latency'

const history = useHistoryStore()
const results = useResultsStore()
const ui = useUIStore()

const keyword = ref('')
const starredOnly = ref(false)
const compareIds = ref<string[]>([])
/** 加载后的提示：这份历史用的是别的参数。 */
const paramNotice = ref('')

onMounted(() => history.refresh())

const filtered = computed(() => {
  const text = keyword.value.trim().toLowerCase()
  return history.entries.filter((entry) => {
    if (starredOnly.value && !entry.starred) return false
    if (text === '') return true
    const haystack = [entry.preset, entry.note, ...(entry.tags ?? []), ...(entry.regions ?? [])]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
    return haystack.includes(text)
  })
})

const diff = computed(() => history.diff)

/**
 * 加载一份历史。
 *
 * 带上当前参数快照：服务端据此判断「这份历史是用别的参数跑的」，用户才不会
 * 拿着两份不可比的结果去做对比。
 */
function load(id: string): void {
  paramNotice.value = ''
  history.load(id, { count: results.total })
}

function toggleCompare(id: string): void {
  compareIds.value = compareIds.value.includes(id)
    ? compareIds.value.filter((item) => item !== id)
    : [...compareIds.value, id].slice(-2)
  if (compareIds.value.length === 2) {
    history.compare(compareIds.value[0]!, compareIds.value[1]!)
  }
}

/** 把加载进来的结果送进结果集并跳到结果页。 */
function applyLoaded(): void {
  const payload = history.loaded
  if (!payload) return
  results.replaceAll(payload.record.results)
  if (payload.paramDiff.length > 0) {
    paramNotice.value = t('history.paramDiff', { minutes: Math.round(payload.ageMinutes) })
  }
  ui.activeView = 'result'
}

function closeDiff(): void {
  history.diff = null
  compareIds.value = []
}
</script>

<template>
  <div class="page">
    <Banner
      v-if="paramNotice"
      tone="warn"
      :message="paramNotice"
      :action-label="t('history.load')"
      @action="applyLoaded"
    />

    <div class="bar">
      <input v-model="keyword" class="ct-input grow" :placeholder="t('common.search')" />
      <label class="toggle">
        <input v-model="starredOnly" type="checkbox" class="ct-check" />
        <span>{{ t('history.starred') }}</span>
      </label>
      <span class="spacer" />
      <span class="ct-subtle tnum">{{ filtered.length }} / {{ history.total }}</span>
      <button type="button" class="ct-btn" @click="history.refresh()">{{ t('common.retry') }}</button>
    </div>

    <div v-if="diff" class="ct-card diff">
      <div class="diff-item">
        <span class="ct-subtle">{{ t('result.stat.avgLatency') }}</span>
        <b class="tnum" :class="diff.latency_delta <= 0 ? 'better' : 'worse'">
          {{ diff.latency_delta > 0 ? '↑' : '↓' }} {{ formatLatency(Math.abs(diff.latency_delta)) }}ms
        </b>
      </div>
      <div class="diff-item"><span class="ct-subtle">新增</span><b class="tnum better">{{ diff.added_count }}</b></div>
      <div class="diff-item"><span class="ct-subtle">消失</span><b class="tnum worse">{{ diff.removed_count }}</b></div>
      <div class="diff-item"><span class="ct-subtle">变化</span><b class="tnum">{{ diff.changed_count }}</b></div>
      <span class="spacer" />
      <button type="button" class="ct-link" @click="closeDiff">{{ t('common.close') }}</button>
    </div>

    <div v-if="filtered.length === 0" class="ct-card empty">
      <p class="empty-title">{{ t('history.empty') }}</p>
      <button type="button" class="ct-btn ct-btn--primary" @click="ui.activeView = 'scan'">
        {{ t('result.emptyAction') }}
      </button>
    </div>
    <HistoryList v-else :entries="filtered" :compare-ids="compareIds" @load="load" @toggle-compare="toggleCompare" />
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
  flex: 1 1 240px;
}

.toggle {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-text-muted);
  font-size: var(--font-size-sm);
  cursor: pointer;
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
</style>
