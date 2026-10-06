<script setup lang="ts">
/**
 * 分组的父行。
 *
 * 父行给的是**决策信息**——这个数据中心值不值得测——而不是明细：几个 IP、
 * 最低延迟、平均延迟、最快下载。子行才是明细，展开即可见。
 *
 * 这一点比「汇总表 + 明细表分开两页」好：明细没有丢失，不需要在汇总与明细
 * 之间来回切页。
 */
import { t } from '@/i18n'
import type { RecordGroup } from '@/stores/results'
import { formatLatency, formatSpeed } from '@/utils/latency'

defineProps<{
  group: RecordGroup
  expanded: boolean
  /** 该组是否已有节点被选中，用于「测速本组」的可用性。 */
  selectable: boolean
  /**
   * 数据列的数量。
   *
   * 必须由外部给：父行要占满整行，而列数随列预设变化。写死一个数字的话，
   * 从「标准」切到「完整」时父行就铺不满了。
   */
  columnCount: number
}>()

const emit = defineEmits<{ (event: 'toggle'): void; (event: 'speed'): void }>()
</script>

<template>
  <tr class="group">
    <td colspan="2">
      <button type="button" class="ct-link" @click="emit('toggle')">
        {{ expanded ? '▾' : '▸' }}
      </button>
    </td>
    <td class="label" colspan="2">
      <b>{{ group.key }}</b>
      <span class="ct-subtle">{{ group.label }}</span>
    </td>
    <!--
      摘要跨到「除按钮之外的最后一列」。合计必须正好等于 3 + 列数：
      多一格会让整行比表格宽一列，浏览器会把表格撑开，后面的列跟着错位。
    -->
    <td :colspan="Math.max(1, columnCount - 2)" class="summary tnum ct-muted">
      {{ t('result.group.summary', {
        count: group.count,
        latency: `${formatLatency(group.minLatency)}ms`,
        speed: `${formatSpeed(group.bestSpeed)}MB/s`,
      }) }}
    </td>
    <td class="right">
      <button type="button" class="ct-btn" :disabled="!selectable" @click="emit('speed')">
        {{ t('result.group.speedThis') }}
      </button>
    </td>
  </tr>
</template>

<style scoped>
.group {
  background: var(--color-surface-sunken);
}

.group td {
  border-bottom: 1px solid var(--color-border);
  font-size: var(--font-size-sm);
}

.label {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
}

.summary {
  font-size: var(--font-size-xs);
}

.right {
  text-align: right;
}
</style>
