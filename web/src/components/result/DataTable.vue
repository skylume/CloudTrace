<script setup lang="ts">
/**
 * 结果表：列由后端字段清单驱动。
 *
 * 模板里只有一层 v-for——从 8 列加到 19 列，这个文件一行都不会变。手写每一列
 * 的写法每加一个字段都要改前端，必然漂移。
 *
 * 行渲染抽在 RecordRow 里，平铺与分组两种模式复用同一份：两处各写一遍的话，
 * 双击复制、信号条、选中态这些细节迟早会不一致。
 */
import { computed, ref } from 'vue'

import GroupRow from './GroupRow.vue'
import RecordRow from './RecordRow.vue'
import { t } from '@/i18n'
import type { FieldDef, IPRecord } from '@/api/types'
import { recordKey, useResultsStore, type SortKey } from '@/stores/results'
import { renderSpec } from '@/utils/recordFormat'

const props = defineProps<{
  columns: FieldDef[]
  records: IPRecord[]
}>()

const results = useResultsStore()

const emit = defineEmits<{ (event: 'speed', records: IPRecord[]): void }>()

/** 展开的行（按 ip:port）。 */
const expandedRows = defineModel<string[]>('expanded', { default: () => [] })
/** 展开的分组（按分组键）。 */
const expandedGroups = ref<string[]>([])

/** 排序：只对后端支持的维度生效，其余字段按地址序兜底。 */
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

function toggleRow(record: IPRecord): void {
  const key = recordKey(record)
  expandedRows.value = expandedRows.value.includes(key)
    ? expandedRows.value.filter((item) => item !== key)
    : [...expandedRows.value, key]
}

function toggleGroup(key: string): void {
  expandedGroups.value = expandedGroups.value.includes(key)
    ? expandedGroups.value.filter((item) => item !== key)
    : [...expandedGroups.value, key]
}

/** 分组模式下的名次仍然连续：展开某个组时序号不能重新从 1 开始。 */
const ranks = computed(() => {
  const map = new Map<string, number>()
  props.records.forEach((record, index) => map.set(recordKey(record), index + 1))
  return map
})

const allSelected = computed(
  () => props.records.length > 0 && props.records.every((record) => results.selected.has(recordKey(record))),
)

function toggleAll(): void {
  if (allSelected.value) results.clearSelection()
  else results.selectAllVisible()
}

/**
 * 测速本组：把该组节点选中，然后交给页面发起测速。
 *
 * 组件不自己发起——发起测速意味着切视图，那是页面的事；组件只表达「用户想
 * 测这一组」。
 */
function selectGroup(records: IPRecord[]): void {
  results.selected = new Set(records.map(recordKey))
  emit('speed', records)
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
        <template v-if="results.groups.length > 0">
          <template v-for="group in results.groups" :key="group.key">
            <GroupRow
              :group="group"
              :expanded="expandedGroups.includes(group.key)"
              :selectable="group.count > 0"
              @toggle="toggleGroup(group.key)"
              @speed="selectGroup(group.records)"
            />
            <template v-if="expandedGroups.includes(group.key)">
              <RecordRow
                v-for="record in group.records"
                :key="recordKey(record)"
                :record="record"
                :columns="props.columns"
                :rank="ranks.get(recordKey(record)) ?? 0"
                :expanded="expandedRows.includes(recordKey(record))"
                @toggle="toggleRow(record)"
              />
            </template>
          </template>
        </template>
        <template v-else>
          <RecordRow
            v-for="record in props.records"
            :key="recordKey(record)"
            :record="record"
            :columns="props.columns"
            :rank="ranks.get(recordKey(record)) ?? 0"
            :expanded="expandedRows.includes(recordKey(record))"
            @toggle="toggleRow(record)"
          />
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

.narrow {
  width: 34px;
  padding: 0 var(--space-2);
}

.num {
  text-align: right;
}
</style>
