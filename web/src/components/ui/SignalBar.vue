<script setup lang="ts">
/**
 * 信号条：全案最核心的视觉识别件。
 *
 * 用「格数 + 颜色 + 数值」三重编码表达延迟。只用颜色是不够的——色盲用户、
 * 灰度打印、高对比度模式下颜色都会失效，格数是唯一始终可读的那一层。
 */
import { computed } from 'vue'

import { formatLatency, latencyTier, tierBars, tierColorVar } from '@/utils/latency'

const props = withDefaults(
  defineProps<{
    /** 延迟，单位毫秒。哨兵值与非法值按「不可达」处理。 */
    latency: number
    /** 数值单位，直接拼在数值后面。 */
    unit?: string
    /** 是否显示数值。只想要图形时关掉。 */
    showValue?: boolean
    /** 数值小数位。 */
    digits?: number
  }>(),
  { unit: 'ms', showValue: true, digits: 1 },
)

const tier = computed(() => latencyTier(props.latency))
const bars = computed(() => tierBars(tier.value))
const color = computed(() => tierColorVar(tier.value))
const text = computed(() => formatLatency(props.latency, props.digits))
const dead = computed(() => text.value === '—')
</script>

<template>
  <span class="signal" :style="{ color }">
    <span class="bars" aria-hidden="true">
      <i v-for="index in 4" :key="index" :class="{ on: index <= bars }" />
    </span>
    <span v-if="showValue" class="val tnum">
      {{ text }}<template v-if="!dead">{{ unit }}</template>
    </span>
    <!-- 格数是给眼睛的，屏幕阅读器读数值就够。 -->
    <span class="sr-only">{{ dead ? '不可达' : `${text} ${unit}` }}</span>
  </span>
</template>

<style scoped>
.signal {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
}

.bars {
  display: inline-flex;
  gap: 2px;
}

.bars i {
  width: 3px;
  height: 11px;
  border-radius: 1.5px;
  background: var(--color-border-strong);
}

.bars i.on {
  background: currentcolor;
}

.val {
  font-weight: 500;
}
</style>
