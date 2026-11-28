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
import { useTaskStore } from '@/stores/conn'

const task = useTaskStore()

const funnelSteps = computed<FunnelStep[]>(() => [
  { label: t('funnel.generated'), value: task.funnel.generated },
  { label: t('funnel.latencyOk'), value: task.funnel.latency_ok },
  { label: t('funnel.regionOk'), value: task.funnel.region_ok },
  { label: t('funnel.usable'), value: task.funnel.usable, tone: 'ok' },
])

const eta = computed(() => {
  const seconds = task.liveEta
  return seconds > 0 ? durationText(seconds) : ''
})

/** 阶段名到文案；后端新增阶段时回退显示原名，不吞掉。 */
const STAGE_KEYS: Record<string, string> = {
  pool: 'stage.pool',
  filter: 'stage.filter',
  probe: 'stage.probe',
  collect: 'stage.collect',
  usability: 'stage.usability',
  measure: 'stage.measure',
}

function stageName(name: string): string {
  const key = STAGE_KEYS[name]
  return key ? t(key) : name
}

function stagePercent(stage: { done: number; total: number }): number {
  if (stage.total <= 0) return 0
  return Math.min(100, Math.round((stage.done / stage.total) * 100))
}
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
      <span class="tnum ct-subtle">{{ task.liveDone }} / {{ task.liveTotal }}</span>
    </div>
    <div class="track">
      <div class="fill" :style="{ width: `${task.percent}%` }" />
    </div>
    <div class="row">
      <span class="ct-subtle">{{ t('task.elapsed') }} <b class="tnum">{{ Math.round(task.liveElapsed) }}s</b></span>
      <span v-if="eta" class="ct-subtle">{{ t('task.eta') }} <b class="tnum">{{ eta }}</b></span>
    </div>
    <ol v-if="task.stages.length" class="stages">
      <li v-for="stage in task.stages" :key="stage.name" class="stage">
        <div class="stage-head">
          <span class="stage-name">{{ stageName(stage.name) }}</span>
          <span class="tnum ct-subtle">{{ stage.done }} / {{ stage.total }}</span>
        </div>
        <div class="stage-track">
          <div class="stage-fill" :style="{ width: `${stagePercent(stage)}%` }" />
        </div>
        <div class="stage-meta ct-subtle">
          <span>{{ t('stage.ok') }} <b class="tnum">{{ stage.ok }}</b></span>
          <span v-if="stage.failed">{{ t('stage.failed') }} <b class="tnum">{{ stage.failed }}</b></span>
          <span v-if="stage.skipped">{{ t('stage.skipped') }} <b class="tnum">{{ stage.skipped }}</b></span>
          <span v-if="stage.elapsed_s">{{ durationText(stage.elapsed_s) }}</span>
        </div>
      </li>
    </ol>
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

.stages {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: var(--space-3) 0 0;
  padding: 0;
  list-style: none;
}

.stage {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.stage-head {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  font-size: var(--font-size-xs);
}

.stage-name {
  font-weight: 600;
}

.stage-head .tnum {
  margin-left: auto;
}

.stage-track {
  height: 4px;
  border-radius: var(--radius-pill);
  background: var(--color-border);
  overflow: hidden;
}

.stage-fill {
  height: 100%;
  border-radius: var(--radius-pill);
  background: var(--color-primary);
  transition: width var(--duration-normal) var(--ease);
}

.stage-meta {
  display: flex;
  gap: var(--space-3);
  font-size: var(--font-size-xs);
}

.stage-meta b {
  font-weight: 600;
}
</style>
