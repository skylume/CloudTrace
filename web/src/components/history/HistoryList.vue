<script setup lang="ts">
/**
 * 历史列表。
 *
 * 卡片而不是表格：每一条历史的信息是「什么时候、用什么档位、多少结果、什么
 * 标签」——这是一组不规则的字段，塞进表格会为了对齐而浪费一半宽度。
 *
 * 列表只读索引：这里显示的每一项都来自索引，打开记录文件是「加载」之后的事。
 */
import { computed } from 'vue'

import { t } from '@/i18n'
import type { HistoryIndexEntry } from '@/api/types'
import { useHistoryStore } from '@/stores/history'
import { useUIStore } from '@/stores/ui'
import { formatLatency, formatSpeed } from '@/utils/latency'
import { ageOf, formatAbsolute, hasAge } from '@/utils/timeText'

const props = defineProps<{
  entries: HistoryIndexEntry[]
  /** 对比模式下已选中的两份。 */
  compareIds: string[]
}>()

const emit = defineEmits<{ (event: 'load', id: string): void; (event: 'toggleCompare', id: string): void }>()

const history = useHistoryStore()
const ui = useUIStore()

/**
 * 删除走软删除 + Toast 撤销，不弹确认框。
 *
 * 弹框是为了防不可逆的操作；删除历史是可逆的（后端有回收站与撤销窗口），
 * 为一个可逆操作打断用户不划算——撤销入口比确认框更轻，也更难点错。
 */
function removeEntry(id: string): void {
  history.remove(id)
  const seconds = Math.round(history.undoWindowMs / 1000)
  ui.pushToast({
    kind: 'warn',
    message: t('history.deleted', { seconds }),
    action: { label: t('common.undo'), run: () => history.undo(id) },
    timeout: history.undoWindowMs,
  })
}

/**
 * 相对时间比绝对时间更有用：「12 分钟前」比「14:05」更能说明新旧。
 *
 * 文案在界面层拼，工具只给单位与数值——单位那几档以前是硬编码的中文，英文
 * 界面下会露出中文。
 */
function relativeTime(createdAt: string): string {
  if (!hasAge(createdAt)) return createdAt
  const age = ageOf(createdAt, Date.now())
  return t(('history.age.' + age.unit) as never, { value: age.value })
}

/** 悬停时给出准确时间，按配置里的时区偏好显示。 */
function exactTime(createdAt: string): string {
  return formatAbsolute(createdAt, ui.timeFormat)
}

const sorted = computed(() =>
  [...props.entries].sort((a, b) => Number(b.starred) - Number(a.starred)),
)
</script>

<template>
  <ul class="list">
    <li v-for="entry in sorted" :key="entry.id" class="item" :class="{ picked: compareIds.includes(entry.id) }">
      <div class="head">
        <button
          type="button"
          class="ct-link star"
          :aria-label="t('history.starred')"
          @click="history.saveTags(entry.id, { starred: !entry.starred })"
        >
          {{ entry.starred ? '★' : '☆' }}
        </button>
        <span class="when" :title="exactTime(entry.created_at)">{{ relativeTime(entry.created_at) }}</span>
        <span v-if="entry.preset" class="chip">{{ entry.preset }}</span>
        <span class="ct-subtle">IPv{{ entry.ip_version }}</span>
        <span class="spacer" />
        <span class="count tnum">{{ t('result.stat.usable') }} {{ entry.count }}</span>
      </div>

      <div class="metrics">
        <span v-if="entry.min_latency" class="ct-muted tnum">
          {{ t('result.stat.minLatency') }} {{ formatLatency(entry.min_latency) }}ms
        </span>
        <span v-if="entry.best_speed" class="ct-muted tnum">
          {{ t('result.stat.bestSpeed') }} {{ formatSpeed(entry.best_speed) }}MB/s
        </span>
        <span v-if="entry.regions?.length" class="ct-subtle">{{ entry.regions.join(' · ') }}</span>
      </div>

      <p v-if="entry.note" class="note ct-subtle">{{ entry.note }}</p>

      <div class="foot">
        <span v-for="tag in entry.tags ?? []" :key="tag" class="chip">{{ tag }}</span>
        <span class="spacer" />
        <button type="button" class="ct-btn" @click="emit('toggleCompare', entry.id)">
          {{ t('history.compare') }}
        </button>
        <button type="button" class="ct-btn" @click="emit('load', entry.id)">{{ t('history.load') }}</button>
        <button type="button" class="ct-btn" @click="removeEntry(entry.id)">{{ t('common.delete') }}</button>
      </div>
    </li>
  </ul>
</template>

<style scoped>
.list {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
}

.item {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border);
  border-top-color: var(--color-border-highlight);
  border-radius: var(--radius-lg);
  background: var(--color-surface);
  box-shadow: var(--elevation-1);
}

.item.picked {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
}

.head,
.metrics,
.foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.head {
  font-size: var(--font-size-sm);
}

.when {
  font-weight: 500;
}

.count {
  font-size: var(--font-size-sm);
}

.metrics {
  font-size: var(--font-size-xs);
}

.note {
  margin: 0;
}

.spacer {
  flex: 1;
}

.star {
  font-size: var(--font-size-md);
  color: var(--color-brand);
}

.chip {
  padding: 2px var(--space-2);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-pill);
  color: var(--color-text-muted);
  font-size: var(--font-size-xs);
}
</style>
