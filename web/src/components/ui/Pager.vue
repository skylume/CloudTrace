<script setup lang="ts">
/**
 * 分页控件。
 *
 * 只发出「跳到第几页」，不自己存页码：页码属于结果集（筛选与排序之后的那个
 * 列表），在组件里再存一份就会有两个真相，而它们在筛选变化时必然对不上。
 */
import { computed } from 'vue'

import { t } from '@/i18n'

const props = defineProps<{
  page: number
  pageCount: number
  /** 筛选之后的总条数，不是全部结果。 */
  total: number
  pageSize: number
}>()

const emit = defineEmits<{
  (event: 'update:page', value: number): void
  (event: 'update:pageSize', value: number): void
}>()

/** 当前显示的是第几条到第几条。没有结果时给 0，而不是 1–0。 */
const from = computed(() => (props.total === 0 ? 0 : (props.page - 1) * props.pageSize + 1))
const to = computed(() => Math.min(props.total, props.page * props.pageSize))

/**
 * 页码按钮：当前页附近最多 5 个。
 *
 * 不做「首页 / 末页」跳转按钮：结果集最多几百条，五页以内就能翻到，多两个
 * 按钮只是占地方。
 */
const pages = computed<number[]>(() => {
  const span = 5
  let start = Math.max(1, props.page - Math.floor(span / 2))
  const end = Math.min(props.pageCount, start + span - 1)
  start = Math.max(1, end - span + 1)

  const out: number[] = []
  for (let i = start; i <= end; i += 1) out.push(i)
  return out
})

/** 可选的每页条数。只给几档：填任意数只会让人犹豫填多少。 */
const SIZES = [20, 50, 100, 200]

/** 当前值不在预设里时（旧版本存过别的数）也照样列出来，否则下拉框会显示空白。 */
const sizes = computed<number[]>(() =>
  SIZES.includes(props.pageSize) ? SIZES : [...SIZES, props.pageSize].sort((a, b) => a - b),
)

function go(next: number): void {
  const clamped = Math.min(Math.max(1, next), props.pageCount)
  if (clamped !== props.page) emit('update:page', clamped)
}

function changeSize(event: Event): void {
  const next = Number((event.target as HTMLSelectElement).value)
  if (Number.isFinite(next) && next > 0) emit('update:pageSize', next)
}
</script>

<template>
  <nav class="pager" :aria-label="t('pager.label')">
    <div class="lead">
      <span class="range">{{ t('pager.range', { from, to, total }) }}</span>
      <label class="size">
        <span>{{ t('pager.perPage') }}</span>
        <select :value="pageSize" @change="changeSize">
          <option v-for="size in sizes" :key="size" :value="size">{{ size }}</option>
        </select>
      </label>
    </div>

    <div class="nav">
      <button type="button" :disabled="page <= 1" @click="go(page - 1)">
        {{ t('pager.prev') }}
      </button>

      <button
        v-for="item in pages"
        :key="item"
        type="button"
        class="num"
        :class="{ on: item === page }"
        :aria-current="item === page ? 'page' : undefined"
        @click="go(item)"
      >
        {{ item }}
      </button>

      <button type="button" :disabled="page >= pageCount" @click="go(page + 1)">
        {{ t('pager.next') }}
      </button>
    </div>
  </nav>
</template>

<style scoped>
.pager {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  flex-wrap: wrap;
  padding: var(--space-2) var(--space-3);
}

.lead {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.range {
  color: var(--color-text-muted);
  font-size: var(--font-size-sm);
  font-variant-numeric: tabular-nums;
}

.size {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--font-size-sm);
}

select {
  padding: var(--space-1) var(--space-2);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  background: var(--color-surface);
  color: var(--color-text);
  font-size: var(--font-size-sm);
}

.nav {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

button {
  min-width: 32px;
  padding: var(--space-1) var(--space-2);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  background: var(--color-surface);
  color: var(--color-text);
  font-size: var(--font-size-sm);
  font-variant-numeric: tabular-nums;
  cursor: pointer;
}

button:hover:not(:disabled) {
  border-color: var(--color-border-strong);
}

button:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.num.on {
  border-color: var(--color-accent);
  color: var(--color-accent);
  font-weight: 600;
}
</style>
