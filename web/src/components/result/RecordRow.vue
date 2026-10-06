<script setup lang="ts">
/**
 * 一行结果（含展开的详情行）。
 *
 * 单独成一个组件是为了让表格在「平铺」与「分组」两种模式下复用同一份行渲染
 * ——两处各写一遍的话，双击复制、信号条、选中态这些细节迟早会不一致。
 *
 * 组件有两个根节点（数据行 + 详情行），这是刻意的：详情要插在数据行下方，
 * 包一层容器就会破坏表格结构。
 */
import { onBeforeUnmount } from 'vue'

import RecordDetail from './RecordDetail.vue'
import SignalBar from '@/components/ui/SignalBar.vue'
import { t } from '@/i18n'
import type { FieldDef, IPRecord } from '@/api/types'
import { recordKey, useResultsStore } from '@/stores/results'
import { useFieldsStore } from '@/stores/fields'
import { formatField, renderSpec } from '@/utils/recordFormat'

const props = defineProps<{
  record: IPRecord
  columns: FieldDef[]
  /** 名次，从 1 开始。 */
  rank: number
  expanded: boolean
}>()

const emit = defineEmits<{
  (event: 'toggle'): void
  (event: 'menu', payload: { record: IPRecord; event: MouseEvent }): void
}>()

const results = useResultsStore()
const fields = useFieldsStore()

const valuesOf = (record: IPRecord) => record as unknown as Record<string, unknown>

/**
 * 点整行也能展开详情。
 *
 * 缓冲一小会儿再切：单元格的双击是「复制这一格」，而双击会先送来两次
 * click——不缓冲的话，用户复制一次会看到详情展开又收回去，闪一下。
 */
const CLICK_DELAY_MS = 220
let pendingToggle: ReturnType<typeof setTimeout> | undefined

function onRowClick(): void {
  if (pendingToggle) clearTimeout(pendingToggle)
  pendingToggle = setTimeout(() => emit('toggle'), CLICK_DELAY_MS)
}

onBeforeUnmount(() => {
  if (pendingToggle) clearTimeout(pendingToggle)
})

/** 双击单元格复制该格内容——拿到结果后九成的动作是复制。 */
async function copyCell(key: string): Promise<void> {
  if (pendingToggle) {
    clearTimeout(pendingToggle)
    pendingToggle = undefined
  }
  const text = formatField(key, valuesOf(props.record)[key], props.record)
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    /* 剪贴板不可用时不打断用户：右键菜单与工具栏都还有复制入口 */
  }
}
</script>

<template>
  <tr
    class="expandable"
    :class="{ picked: results.selected.has(recordKey(props.record)) }"
    @click="onRowClick"
    @contextmenu.prevent="emit('menu', { record: props.record, event: $event })"
  >
    <!-- 勾选框自己吃掉点击：顺手勾一下不该把详情展开。 -->
    <td class="narrow ct-sticky ct-sticky-1" @click.stop>
      <input
        type="checkbox"
        class="ct-check"
        :checked="results.selected.has(recordKey(props.record))"
        :aria-label="props.record.ip"
        @change="results.toggleSelect(recordKey(props.record))"
      />
    </td>
    <td class="narrow ct-sticky ct-sticky-2" @click.stop>
      <button
        type="button"
        class="ct-link expand"
        :aria-expanded="props.expanded"
        :aria-label="t(props.expanded ? 'result.collapse' : 'result.expand')"
        @click="emit('toggle')"
      >
        {{ props.expanded ? '▾' : '▸' }}
      </button>
    </td>
    <td class="narrow num tnum ct-sticky ct-sticky-3">{{ props.rank }}</td>
    <td
      v-for="(column, index) in props.columns"
      :key="column.key"
      :class="{ num: renderSpec(column.key).align === 'right', 'ct-sticky ct-sticky-4': index === 0 }"
      @dblclick="copyCell(column.key)"
    >
      <SignalBar v-if="renderSpec(column.key).render === 'latency'" :latency="props.record.latency" />
      <span
        v-else
        :class="{
          tnum: renderSpec(column.key).render === 'number',
          mono: renderSpec(column.key).render === 'mono',
        }"
      >
        {{ formatField(column.key, valuesOf(props.record)[column.key], props.record) }}
      </span>
    </td>
  </tr>
  <tr v-if="props.expanded" class="detail-row">
    <td :colspan="props.columns.length + 3">
      <RecordDetail :record="props.record" :fields="fields.fields" />
    </td>
  </tr>
</template>

<style scoped>
.narrow {
  width: 34px;
  padding: 0 var(--space-2);
}

/* 整行可点，指针提示一下；单元格里的文字仍可正常选中。 */
.expandable {
  cursor: pointer;
}

.expand {
  font-size: var(--font-size-sm);
  line-height: 1;
}

.num {
  text-align: right;
}

.detail-row td {
  height: auto;
  padding: 0;
  background: var(--color-surface-sunken);
}
</style>
