<script setup lang="ts">
/**
 * 过程面板：进度、漏斗、地区分布。
 *
 * 「过程必须可见」的落点——用户对等待极度不耐受，超过一分钟会以为卡死。
 * 四重反馈里这里承担三重（阶段名、进度、漏斗），日志在另一个组件里。
 *
 * 直接连 store 而不是层层透传 props：这些数据本来就只有一处来源，
 * 透传只会让页面模板变长，而页面模板有行数上限。
 */
import { computed } from 'vue'

import Funnel, { type FunnelStep } from '@/components/ui/Funnel.vue'
import RegionChips from '@/components/result/RegionChips.vue'
import RadarPulse from '@/components/ui/RadarPulse.vue'
import { durationText, t } from '@/i18n'
import { useTaskStore } from '@/stores/task'

const task = useTaskStore()

const funnelSteps = computed<FunnelStep[]>(() => [
  { label: t('funnel.generated'), value: task.funnel.generated },
  { label: t('funnel.latencyOk'), value: task.funnel.latency_ok },
  { label: t('funnel.regionOk'), value: task.funnel.region_ok },
  { label: t('funnel.usable'), value: task.funnel.usable, tone: 'ok' },
])

const eta = computed(() => {
  const seconds = task.state.eta_s
  return seconds > 0 ? durationText(seconds) : ''
})
</script>

<template>
  <section class="ct-card">
    <h2 class="ct-card-title">
      <RadarPulse v-if="task.running" :size="22" />
      <span>{{ t('scan.progress') }}</span>
    </h2>
    <div class="row">
      <span class="ct-subtle">{{ t(`task.phase.${task.phase}`) }}</span>
      <span class="spacer" />
      <span class="tnum ct-subtle">{{ task.state.done }} / {{ task.state.total }}</span>
    </div>
    <div class="track">
      <div class="fill" :style="{ width: `${task.percent}%` }" />
    </div>
    <div class="row">
      <span class="ct-subtle">{{ t('task.elapsed') }} <b class="tnum">{{ Math.round(task.state.elapsed_s) }}s</b></span>
      <span v-if="eta" class="ct-subtle">{{ t('task.eta') }} <b class="tnum">{{ eta }}</b></span>
    </div>
  </section>

  <section class="ct-card">
    <h2 class="ct-card-title">{{ t('scan.funnel') }}</h2>
    <Funnel :steps="funnelSteps" />
  </section>

  <section class="ct-card">
    <h2 class="ct-card-title">{{ t('scan.regions') }}</h2>
    <!-- 与结果页共用同一个组件与同一个筛选状态，两处不会各说各话。 -->
    <RegionChips :limit="12" />
  </section>
</template>

<style scoped>
/* 标题里的雷达贴着文字左边，不额外占高度。 */
.ct-card-title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.spacer {
  flex: 1;
}

.row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  font-size: var(--font-size-xs);
}

.track {
  height: 6px;
  margin: var(--space-2) 0;
  border-radius: var(--radius-pill);
  background: var(--color-border);
  overflow: hidden;
}

.fill {
  height: 100%;
  border-radius: var(--radius-pill);
  background: var(--color-primary);
  transition: width var(--duration-normal) var(--ease);
}



</style>
