<script setup lang="ts">
/**
 * 结果表：列由后端字段清单驱动。
 *
 * 模板里只有一层 `v-for="column in columns"`——从 8 列加到 19 列，这个文件
 * 一行都不会变。手写每一列的写法每加一个字段都要改前端，必然漂移。
 *
 * 表格本身不做虚拟滚动：结果集默认几百条，分页足够；虚拟滚动等真有上万条
 * 的用例再加，过早引入会让「双击复制」「行内展开」都变复杂。
 */
import { computed } from 'vue'

import RecordDetail from './RecordDetail.vue'
import SignalBar from '@/components/ui/SignalBar.vue'
import { t } from '@/i18n'
import type { FieldDef, IPRecord } from '@/api/types'
import { formatField, renderSpec } from '@/utils/recordFormat'
import { useFieldsStore } from '@/stores/fields'
import { recordKey, useResultsStore, type SortKey } from '@/stores/results'

const props = defineProps<{
  columns: FieldDef[]
  records: IPRecord[]
  /** 名次起点，分页时用于显示序号。 */
  offset?: number
}>()

const results = useResultsStore()
const fields = useFieldsStore()

const expanded = defineModel<string[]>('expanded', { default: () => [] })

const valuesOf = (record: IPRecord) => record as unknown as Record<string, unknown>

/** 排序：点表头切换；只对后端支持的维度生效，其余字段按地址序兜底。 */
const SORTABLE: Record<string, SortKey> = {
  latency: 'latency',
  latency_avg: 'latency_avg',
  loss: 'loss',
  jitter: 'jitter',
  speed_mbps: 'speed_mbps',
  score: 'score',
  colo: 'region',
  region_name: 'region',
}

function sortBy(key: string): void {
  const target = SORTABLE[key]
  if (!target) return
  if (results.sortKey === target) {
    results.sortDesc = !results.sortDesc
    return
  }
  results.sortKey = target
  results.sortDesc = target === 'speed_mbps' || target === 'score'
}

function isSorted(key: string): 'asc' | 'desc' | '' {
  if (SORTABLE[key] !== results.sortKey) return ''
  return results.sortDesc ? 'desc' : 'asc'
}

function toggleExpand(record: IPRecord): void {
  const key = recordKey(record)
  expanded.value = expanded.value.includes(key)
    ? expanded.value.filter((item) => item !== key)
    : [...expanded.value, key]
}

/** 双击单元格复制该格内容——拿到结果后九成的动作是复制。 */
async function copyCell(record: IPRecord, key: string): Promise<void> {
  const text = formatField(key, valuesOf(record)[key], record)
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    /* 剪贴板不可用时不打断用户，右键菜单还有复制整行 */
  }
}

const allSelected = computed(
  () => props.records.length > 0 && props.records.every((record) => results.selected.has(recordKey(record))),
)

function toggleAll(): void {
  if (allSelected.value) results.clearSelection()
  else results.selectAllVisible()
}
</script>

<template>
  <div class="wrap">
    <table class="table">
      <thead>
        <tr>
          <th class="narrow">
            <input
              type="checkbox"
              class="ct-check"
              :checked="allSelected"
              :aria-label="t('result.selectAll')"
              @change="toggleAll"
            />
          </th>
          <th class="narrow" />
          <th class="narrow num">#</th>
          <th
            v-for="column in props.columns"
            :key="column.key"
            :class="{ sortable: SORTABLE[column.key], num: renderSpec(column.key).align === 'right' }"
            @click="sortBy(column.key)"
          >
            {{ column.label }}
            <span v-if="isSorted(column.key)" class="arrow">{{ isSorted(column.key) === 'desc' ? '↓' : '↑' }}</span>
          </th>
        </tr>
      </thead>
      <tbody>
        <template v-for="(record, index) in props.records" :key="recordKey(record)">
          <tr :class="{ picked: results.selected.has(recordKey(record)) }">
            <td class="narrow">
              <input
                type="checkbox"
                class="ct-check"
                :checked="results.selected.has(recordKey(record))"
                :aria-label="record.ip"
                @change="results.toggleSelect(recordKey(record))"
              />
            </td>
            <td class="narrow">
              <button
                type="button"
                class="ct-link"
                :aria-label="t('result.expand')"
                @click="toggleExpand(record)"
              >
                {{ expanded.includes(recordKey(record)) ? '▾' : '▸' }}
              </button>
            </td>
            <td class="narrow num tnum">{{ (props.offset ?? 0) + index + 1 }}</td>
            <td
              v-for="column in props.columns"
              :key="column.key"
              :class="{ num: renderSpec(column.key).align === 'right' }"
              @dblclick="copyCell(record, column.key)"
            >
              <SignalBar v-if="renderSpec(column.key).render === 'latency'" :latency="record.latency" />
              <span
                v-else
                :class="{
                  tnum: renderSpec(column.key).render === 'number',
                  mono: renderSpec(column.key).render === 'mono',
                }"
              >
                {{ formatField(column.key, valuesOf(record)[column.key], record) }}
              </span>
            </td>
          </tr>
          <tr v-if="expanded.includes(recordKey(record))" class="detail-row">
            <td :colspan="props.columns.length + 3">
              <RecordDetail :record="record" :fields="fields.fields" />
            </td>
          </tr>
        </template>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.wrap {
  overflow-x: auto;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  background: var(--color-surface);
}

.table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--font-size-sm);
}

th,
td {
  padding: 0 var(--space-3);
  height: var(--row-height);
  text-align: left;
  white-space: nowrap;
  border-bottom: 1px solid var(--color-border);
}

th {
  position: sticky;
  top: 0;
  z-index: 1;
  height: 34px;
  background: var(--color-surface-sunken);
  color: var(--color-text-subtle);
  font-size: var(--font-size-xs);
  font-weight: 500;
}

th.sortable {
  cursor: pointer;
  user-select: none;
}

th.sortable:hover {
  color: var(--color-text);
}

.arrow {
  color: var(--color-primary-text);
}

tbody tr:hover {
  background: var(--color-surface-hover);
}

/* 选中行左侧一道主色竖条：比整行涂色轻，但足够把「选中的是哪些」说清楚。 */
tbody tr.picked {
  background: var(--color-primary-soft);
}

tbody tr.picked td:first-child {
  box-shadow: inset 2px 0 0 var(--color-primary);
}

.detail-row td {
  height: auto;
  padding: 0;
  background: var(--color-surface-sunken);
}

.narrow {
  width: 34px;
  padding: 0 var(--space-2);
}

.num {
  text-align: right;
}
</style>
