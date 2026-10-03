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
import { formatLatency, formatSpeed } from '@/utils/latency'

const props = defineProps<{
  entries: HistoryIndexEntry[]
  /** 对比模式下已选中的两份。 */
  compareIds: string[]
}>()

const emit = defineEmits<{ (event: 'load', id: string): void; (event: 'toggleCompare', id: string): void }>()

const history = useHistoryStore()

/** 相对时间比绝对时间更有用：「12 分钟前」比「14:05」更能说明新旧。 */
function relativeTime(createdAt: string): string {
  const at = new Date(createdAt).getTime()
  if (!Number.isFinite(at)) return createdAt
  const minutes = Math.round((Date.now() - at) / 60000)
  if (minutes < 1) return t('history.age', { minutes: 1 })
  if (minutes < 60) return t('history.age', { minutes })
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  return `${Math.round(hours / 24)} 天前`
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
        <span class="when">{{ relativeTime(entry.created_at) }}</span>
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
        <button type="button" class="ct-btn" @click="history.remove(entry.id)">{{ t('common.delete') }}</button>
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
