<script setup lang="ts">
/**
 * 漏斗：把扫描的四段过滤过程画出来。
 *
 * 四段等宽、下划线长度递减——用长度表达「逐级收窄」，比四个孤立的数字更能
 * 说明「大部分候选是被筛掉的」。这正是「过程必须可见」的意义。
 *
 * 组件不假设四段的含义，标签与数值都由调用方给：两阶段扫描时会换成
 * 粗扫/精扫两组，含义不同但形态一致。
 */
import { computed } from 'vue'

export interface FunnelStep {
  label: string
  value: number
  /** ok 用于最后一段（可用），与前面几段的「被筛掉」区分开。 */
  tone?: 'default' | 'ok'
}

const props = defineProps<{ steps: FunnelStep[] }>()

/** 下划线长度按段序递减，表达收窄。 */
const WIDTHS = ['100%', '72%', '54%', '26%']

const items = computed(() =>
  props.steps.map((step, index) => ({ ...step, width: WIDTHS[index] ?? '26%' })),
)
</script>

<template>
  <div class="funnel">
    <div v-for="item in items" :key="item.label" class="step">
      <div class="lbl">{{ item.label }}</div>
      <div class="num tnum" :class="{ ok: item.tone === 'ok' }">{{ item.value }}</div>
      <div class="bar" :class="{ ok: item.tone === 'ok' }" :style="{ width: item.width }" />
    </div>
  </div>
</template>

<style scoped>
.funnel {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-3);
}

.step {
  min-width: 0;
}

.lbl {
  margin-bottom: 2px;
  color: var(--color-text-muted);
  font-size: var(--font-size-xs);
}

.num {
  font-size: var(--font-size-2xl);
  font-weight: 500;
  letter-spacing: -0.02em;
  line-height: 1.15;
}

.num.ok {
  color: var(--color-ok);
}

.bar {
  height: 3px;
  margin-top: var(--space-2);
  border-radius: 2px;
  background: var(--color-primary);
  opacity: 0.85;
  transition: width var(--duration-normal) var(--ease);
}

.bar.ok {
  background: var(--color-ok);
}
</style>
