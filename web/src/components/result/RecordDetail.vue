<script setup lang="ts">
/**
 * 行内展开的详情卡：把全部字段铺开。
 *
 * 为什么不做成「更多列」：列宽是稀缺资源，展开是免费的。想看细节的人点开，
 * 不想看的人不受干扰——19 个字段同时可达，但不牺牲扫读性。
 *
 * 字段清单来自后端，这里只负责摆成两列网格；`trace` 单独成块，因为它是一组
 * 键值对而不是一个值。
 */
import { computed } from 'vue'

import { t } from '@/i18n'
import type { FieldDef, IPRecord } from '@/api/types'
import { formatField, renderSpec } from '@/utils/recordFormat'

const props = defineProps<{
  record: IPRecord
  /** 后端下发的字段清单；未加载时给空数组，详情里就只显示 trace。 */
  fields: FieldDef[]
}>()

const values = computed<Record<string, unknown>>(() => props.record as unknown as Record<string, unknown>)

/** trace 是嵌套对象，单独渲染。 */
const traceEntries = computed(() => {
  const trace = props.record.trace
  if (!trace) return []
  return Object.entries(trace)
})

const plainFields = computed(() => props.fields.filter((field) => field.key !== 'trace'))
</script>

<template>
  <div class="detail">
    <div class="grid">
      <div v-for="field in plainFields" :key="field.key" class="cell">
        <span class="label">{{ field.label }}</span>
        <span class="value" :class="{ mono: renderSpec(field.key).render === 'mono' }">
          {{ formatField(field.key, values[field.key], record) }}
        </span>
      </div>
    </div>

    <div class="trace">
      <div class="trace-title">{{ t('result.traceTitle') }}</div>
      <p v-if="traceEntries.length === 0" class="ct-subtle">{{ t('result.noTrace') }}</p>
      <div v-else class="trace-body ct-mono">
        <span v-for="[key, value] in traceEntries" :key="key" class="pair">
          <span class="key">{{ key }}</span>=<span class="val">{{ value }}</span>
        </span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.detail {
  padding: var(--space-4);
  background: var(--color-surface-sunken);
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: var(--space-2) var(--space-5);
}

.cell {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  min-width: 0;
  font-size: var(--font-size-sm);
}

.label {
  flex: 0 0 96px;
  color: var(--color-text-subtle);
  font-size: var(--font-size-xs);
}

.value {
  min-width: 0;
  overflow-wrap: anywhere;
}

.trace {
  margin-top: var(--space-4);
  padding-top: var(--space-3);
  border-top: 1px solid var(--color-border);
}

.trace-title {
  margin-bottom: var(--space-2);
  color: var(--color-text-subtle);
  font-size: var(--font-size-xs);
}

.trace-body {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-4);
  font-size: var(--font-size-xs);
  line-height: 1.9;
}

.pair .key {
  color: var(--color-text-subtle);
}
</style>
