<script setup lang="ts">
/**
 * 地区芯片：多选过滤。
 *
 * 扫描页与结果页共用同一个组件、同一个筛选状态（`results.regionFilter`）。
 * 两处各写一份的话，用户在扫描页选完地区、切到结果页会看到另一套筛选条件——
 * 而这两处本来就是同一个动作的两个入口。
 *
 * 芯片上带数量而不是只给名字：用户选地区时真正想知道的是「这里有几个」，
 * 一个 0 个节点的地区不值得点。
 */
import { computed } from 'vue'

import { t } from '@/i18n'
import { useResultsStore } from '@/stores/results'

const props = defineProps<{
  /** 最多显示几个。按数量降序取，长尾地区不该把这一行撑成三行。 */
  limit?: number
}>()

const results = useResultsStore()

const chips = computed(() => results.regionCounts.slice(0, props.limit ?? 16))

function toggle(code: string): void {
  const current = results.regionFilter
  results.regionFilter = current.includes(code)
    ? current.filter((item) => item !== code)
    : [...current, code]
}
</script>

<template>
  <div v-if="chips.length > 0" class="chips">
    <span class="ct-subtle label">{{ t('scan.regions') }}</span>
    <button
      v-for="[code, count] in chips"
      :key="code"
      type="button"
      class="chip"
      :class="{ on: results.regionFilter.includes(code) }"
      :aria-pressed="results.regionFilter.includes(code)"
      @click="toggle(code)"
    >
      {{ code }} <span class="tnum count">{{ count }}</span>
    </button>
    <button v-if="results.regionFilter.length > 0" type="button" class="ct-link" @click="results.regionFilter = []">
      {{ t('result.filter.clear') }}
    </button>
  </div>
</template>

<style scoped>
.chips {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1);
}

.label {
  margin-right: var(--space-1);
  font-size: var(--font-size-xs);
}

.chip {
  padding: 3px var(--space-3);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-pill);
  background: var(--color-surface);
  color: var(--color-text-muted);
  font-size: var(--font-size-xs);
  cursor: pointer;
}

.chip:hover {
  border-color: var(--color-primary);
  color: var(--color-text);
}

.chip.on {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
  color: var(--color-primary-text);
}

.count {
  color: var(--color-text-subtle);
}

.chip.on .count {
  color: inherit;
}
</style>
