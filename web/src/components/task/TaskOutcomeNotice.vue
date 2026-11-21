<script setup lang="ts">
/**
 * 任务结局：失败说清停在哪一步、跑了多久；有非致命问题就列出来。
 *
 * 数据来自 task store 的 outcome——它在终止事件到达时写入、下一轮任务开始时
 * 清空，所以这里不必自己判断「这是不是上次的旧消息」。
 */
import { computed } from 'vue'

import Banner from '@/components/ui/Banner.vue'
import { durationMsText, phaseText, t } from '@/i18n'
import type { TaskOutcome } from '@/stores/task'

const props = defineProps<{ outcome: TaskOutcome | null }>()

const failed = computed(() => props.outcome?.status === 'failed')
const warnings = computed(() => props.outcome?.warnings ?? [])
const phase = computed(() => phaseText(props.outcome?.stage ?? ''))

const title = computed(() =>
  phase.value === '' ? t('task.failedTitlePlain') : t('task.failedTitle', { phase: phase.value }),
)

/** 没有阶段就直说不知道停在哪，别编一个出来。 */
const detail = computed(() => {
  const outcome = props.outcome
  if (!outcome || phase.value === '') return t('task.failedPlain')
  if (outcome.durationMs <= 0) return t('task.failedStageOnly', { phase: phase.value })
  return t('task.failedDetail', {
    phase: phase.value,
    duration: durationMsText(outcome.durationMs),
  })
})
</script>

<template>
  <Banner v-if="failed" tone="bad" :message="title" :note="detail" :closable="false" />

  <!-- 默认展开：告警是「这次结果怎么用」的一部分；觉得吵可以随手收起。 -->
  <details v-if="warnings.length > 0" class="ct-card warn-box" open>
    <summary class="warn-summary">
      <span class="warn-count">{{ t('task.warnCount', { count: warnings.length }) }}</span>
      <span class="warn-note">{{ t('task.warnNote') }}</span>
    </summary>
    <ul class="warn-list">
      <li v-for="(warning, index) in warnings" :key="index">{{ warning }}</li>
    </ul>
  </details>
</template>

<style scoped>
.warn-box {
  border-color: var(--color-warn-border);
  border-top-color: var(--color-warn-border);
  background: var(--color-warn-bg);
  color: var(--color-warn);
  font-size: var(--font-size-sm);
  overflow-wrap: anywhere;
}

.warn-summary {
  cursor: pointer;
}

.warn-count {
  font-weight: 500;
}

.warn-note {
  margin-left: var(--space-2);
  font-size: var(--font-size-xs);
  opacity: 0.75;
}

.warn-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: var(--space-2) 0 0;
  padding-left: var(--space-5);
}
</style>
