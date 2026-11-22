<script setup lang="ts">
/**
 * 零结果回看。
 *
 * 扫出 0 条时最需要回答的是「条件太严，还是这批地址根本不通」。把最接近阈值
 * 的那几条摆出来，用户看一眼就知道该不该放宽，不必再猜着重跑一次。
 */
import { computed } from 'vue'

import type { IPRecord } from '@/api/types'
import { t } from '@/i18n'
import { formatLatency, formatLoss } from '@/utils/latency'

const props = defineProps<{ threshold: number; items: IPRecord[] }>()
const emit = defineEmits<{
  (event: 'retry', threshold: number): void
  (event: 'back'): void
}>()

/** 最短的那条：建议值从它推出来，文案里报的也是它。 */
const best = computed(() => {
  let min = Number.POSITIVE_INFINITY
  for (const item of props.items) {
    if (item.latency >= 0) min = Math.min(min, item.latency)
  }
  return Number.isFinite(min) ? min : -1
})

/** 建议阈值：在最短那条之上再留一成余量。 */
const suggested = computed(() => (best.value >= 0 ? Math.max(1, Math.ceil(best.value * 1.1)) : 0))

function retry(): void {
  if (suggested.value > 0) emit('retry', suggested.value)
}
</script>

<template>
  <section class="nearest">
    <p class="title">{{ t('result.nearestTitle') }}</p>
    <p class="ct-subtle">{{ t('result.nearestDesc', { threshold, count: items.length }) }}</p>
    <p v-if="suggested > 0" class="suggest">
      {{ t('result.nearestSuggest', { latency: formatLatency(best), suggested }) }}
    </p>
    <table class="tbl">
      <thead>
        <tr>
          <th>{{ t('result.nearestIp') }}</th>
          <th class="num">{{ t('result.nearestLatency') }}</th>
          <th class="num">{{ t('result.nearestLoss') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="item in items" :key="`${item.ip}:${item.port}`">
          <td class="ct-mono">{{ item.ip }}:{{ item.port }}</td>
          <td class="num tnum">{{ formatLatency(item.latency) }}</td>
          <td class="num tnum">{{ formatLoss(item.loss, item.sent) }}</td>
        </tr>
      </tbody>
    </table>
    <div class="actions">
      <button v-if="suggested > 0" type="button" class="ct-btn ct-btn--primary" @click="retry">
        {{ t('result.nearestRetry', { value: suggested }) }}
      </button>
      <button type="button" class="ct-btn" @click="emit('back')">{{ t('result.nearestBack') }}</button>
    </div>
  </section>
</template>

<style scoped>
.nearest {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-5) var(--space-4);
}

.title {
  font-weight: 500;
}

.suggest {
  color: var(--color-warn);
}

.tbl {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--font-size-sm);
}

.tbl th,
.tbl td {
  padding: var(--space-1) var(--space-2);
  border-bottom: 1px solid var(--color-border);
  text-align: left;
}

.tbl .num {
  text-align: right;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-2);
}
</style>
