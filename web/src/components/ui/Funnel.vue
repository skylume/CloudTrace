<script setup lang="ts">
/**
 * 漏斗：把扫描的四段过滤过程画出来。
 *
 * 四段等宽、下划线长度递减——用长度表达「逐级收窄」，比四个孤立的数字更能
 * 说明「大部分候选是被筛掉的」。这正是「过程必须可见」的意义。
 *
 * 组件不假设四段的含义，标签与数值都由调用方给：两阶段扫描时会换成
 * 粗扫/精扫两组，含义不同但形态一致。
 *
 * 可点与否只看调用方传没传 select 监听，与尺寸无关：结果页用紧凑版但可点，
 * 扫描页过程面板不传监听，自然就是静态的。
 */
import { computed, getCurrentInstance } from 'vue'

import { t } from '@/i18n'

export interface FunnelStep {
  /** 段的稳定标识；可点时用它回传，不依赖展示文案。 */
  key?: string
  label: string
  value: number
  /** ok 用于最后一段（可用），与前面几段的「被筛掉」区分开。 */
  tone?: 'default' | 'ok'
  /**
   * 当前视图最匹配这一段。只作高亮，不禁用其他段——四段是并列的观察角度，
   * 不是必须按顺序走的步骤。
   */
  active?: boolean
}

const props = defineProps<{
  steps: FunnelStep[]
  /**
   * 紧凑版：数字降到与正文同级。
   *
   * 扫描页右栏是「过程面板」，漏斗是主角，数字用 2xl 撑起来；结果页的漏斗只是
   * 一行说明「这些数字是怎么筛出来的」，用同样的字号会把统计卡压下去。
   * 只压字号与条高，不影响能否点击。
   */
  compact?: boolean
}>()

const emit = defineEmits<{ (event: 'select', key: string): void }>()

/** 声明了 emit 之后监听器不会出现在 attrs 里，只能从 vnode props 认。 */
const instance = getCurrentInstance()
const clickable = computed(() => typeof instance?.vnode.props?.onSelect === 'function')

/** 下划线长度按段序递减，表达收窄。 */
const WIDTHS = ['100%', '72%', '54%', '26%']

const items = computed(() =>
  props.steps.map((step, index) => ({ ...step, width: WIDTHS[index] ?? '26%' })),
)

function onSelect(item: FunnelStep): void {
  if (!clickable.value || !item.key) return
  emit('select', item.key)
}
</script>

<template>
  <div
    class="funnel"
    :class="{ compact: props.compact }"
    :style="{ gridTemplateColumns: `repeat(${items.length}, minmax(0, 1fr))` }"
  >
    <component
      :is="clickable ? 'button' : 'div'"
      v-for="item in items"
      :key="item.key ?? item.label"
      class="step"
      :class="{ clickable, active: item.active }"
      v-bind="clickable ? { type: 'button' } : {}"
      :title="clickable ? t('funnel.selectHint') : undefined"
      :aria-label="clickable ? `${item.label}：${t('funnel.selectHint')}` : undefined"
      :aria-pressed="clickable ? (item.active ? 'true' : 'false') : undefined"
      @click="onSelect(item)"
    >
      <span class="lbl">{{ item.label }}</span>
      <span class="num tnum" :class="{ ok: item.tone === 'ok' }">{{ item.value }}</span>
      <span class="bar" :class="{ ok: item.tone === 'ok' }" :style="{ width: item.width }" />
    </component>
  </div>
</template>

<style scoped>
.funnel {
  display: grid;
  gap: var(--space-3);
}

/* 紧凑版：数字与标签同一量级，整块高度降到一半左右。 */
.funnel.compact .num {
  font-size: var(--font-size-lg);
}

.funnel.compact .bar {
  margin-top: var(--space-1);
}

.step {
  display: block;
  min-width: 0;
  padding: 0;
  border: 0;
  background: none;
  color: inherit;
  font: inherit;
  text-align: left;
}

.step.clickable {
  cursor: pointer;
}

.step.clickable:hover .num {
  color: var(--color-primary);
}

.step.clickable:hover .bar {
  opacity: 1;
}

.step.clickable:focus-visible {
  border-radius: var(--radius-sm);
  outline: 2px solid var(--color-primary);
  outline-offset: 3px;
}

.lbl {
  display: block;
  margin-bottom: 2px;
  color: var(--color-text-muted);
  font-size: var(--font-size-xs);
}

.num {
  display: block;
  font-size: var(--font-size-2xl);
  font-weight: 500;
  letter-spacing: -0.02em;
  line-height: 1.15;
}

.num.ok {
  color: var(--color-ok);
}

.bar {
  display: block;
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

/*
 * 高亮用字重与下划线，不覆盖 ok 段的语义色，也不加高条高——
 * 紧凑版只有一行高度，抬高下划线会把整块撑开。
 */
.step.active .lbl {
  color: var(--color-text);
  font-weight: 600;
  text-decoration: underline;
  text-underline-offset: 3px;
}

.step.active .num {
  font-weight: 600;
}

.step.active .bar {
  opacity: 1;
}
</style>
