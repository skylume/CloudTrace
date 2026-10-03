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
import { t } from '@/i18n'
import { useResultsStore } from '@/stores/results'
import { useTaskStore } from '@/stores/task'

const task = useTaskStore()
const results = useResultsStore()

const funnelSteps = computed<FunnelStep[]>(() => [
  { label: t('funnel.generated'), value: task.funnel.generated },
  { label: t('funnel.latencyOk'), value: task.funnel.latency_ok },
  { label: t('funnel.regionOk'), value: task.funnel.region_ok },
  { label: t('funnel.usable'), value: task.funnel.usable, tone: 'ok' },
])

/** 地区芯片：点一下把该地区加进筛选。 */
const regions = computed(() => results.regionCounts.slice(0, 12))

const eta = computed(() => {
  const seconds = task.state.eta_s
  if (!seconds || seconds <= 0) return ''
  if (seconds < 60) return `${Math.round(seconds)} 秒`
  const minutes = Math.floor(seconds / 60)
  return `${minutes} 分 ${Math.round(seconds - minutes * 60)} 秒`
})

function toggleRegion(code: string): void {
  const current = results.regionFilter
  results.regionFilter = current.includes(code)
    ? current.filter((item) => item !== code)
    : [...current, code]
}
</script>

<template>
  <section class="ct-card">
    <h2 class="ct-card-title">{{ t('scan.progress') }}</h2>
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
    <h2 class="ct-card-title">
      <span>{{ t('scan.regions') }}</span>
      <span class="spacer" />
      <button v-if="results.regionFilter.length > 0" type="button" class="ct-link" @click="results.regionFilter = []">
        {{ t('result.filter.clear') }}
      </button>
    </h2>
    <p v-if="regions.length === 0" class="ct-subtle">{{ t('common.empty') }}</p>
    <div v-else class="chips">
      <button
        v-for="[code, count] in regions"
        :key="code"
        type="button"
        class="chip"
        :class="{ on: results.regionFilter.includes(code) }"
        @click="toggleRegion(code)"
      >
        {{ code }} <span class="tnum">{{ count }}</span>
      </button>
    </div>
  </section>
</template>

<style scoped>
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

.chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
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

.chip.on {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
  color: var(--color-primary-text);
}
</style>
