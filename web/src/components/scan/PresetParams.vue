<script setup lang="ts">
/**
 * 扫描参数面板。
 *
 * 只负责「一个参数一行」的渲染与取值：标签、范围、说明都来自 i18n 映射表，
 * 这里不硬编码任何一条。档位、联动提示、另存为都在外面，这个组件不认识它们。
 *
 * 超出建议区间**只标色不拦截**：用户有权这么设，但该知道代价。
 */
import { computed } from 'vue'

import { t } from '@/i18n'
import { SCAN_PARAMS } from '@/i18n/params'
import { useAdaptiveStore } from '@/stores/adaptive'
import { useUIStore } from '@/stores/ui'

const params = defineModel<Record<string, number | boolean>>('params', { required: true })

defineProps<{ disabled?: boolean }>()

const ui = useUIStore()
const adaptive = useAdaptiveStore()

/** 摘要里显示的参数；展开后显示全部。 */
const visible = computed(() => (ui.showAdvanced ? SCAN_PARAMS : SCAN_PARAMS.filter((spec) => spec.summary)))

/**
 * 是否超出建议区间。
 *
 * 只看有值的情况：0 在探测次数里表示「自动」，不是「太小」。
 */
function outOfRange(spec: (typeof SCAN_PARAMS)[number]): boolean {
  const value = Number(params.value[spec.key] ?? 0)
  if (!Number.isFinite(value)) return false
  if (value === 0 && spec.kind === 'int') return false
  if (spec.warnBelow !== undefined && value < spec.warnBelow) return true
  if (spec.warnAbove !== undefined && value > spec.warnAbove) return true
  return false
}

/** 越界的输入夹回范围内：宁可夹住，也不要让一个不可能的值得以发出去。 */
function onNumber(key: string, raw: string, spec: { min?: number; max?: number }): void {
  const value = Number(raw)
  if (!Number.isFinite(value)) return
  const clamped = Math.min(Math.max(value, spec.min ?? 0), spec.max ?? Number.MAX_SAFE_INTEGER)
  params.value = { ...params.value, [key]: clamped }
}
</script>

<template>
  <div class="grid">
    <label v-for="spec in visible" :key="spec.key" class="field">
      <span class="label" :title="t(spec.hintKey as never)">
        {{ t(spec.labelKey as never) }}
        <!-- 被自动调整过的值必须留痕：徽标 + 悬停说明 + 一键还原。 -->
        <button
          v-if="adaptive.applied[spec.key]"
          type="button"
          class="badge"
          :title="adaptive.reasonText(adaptive.applied[spec.key]!.reason)"
          @click="adaptive.revert(spec.key)"
        >
          {{ t('adaptive.badge') }}
        </button>
      </span>
      <input
        v-if="spec.kind === 'int'"
        class="ct-input tnum"
        :class="{ warn: outOfRange(spec) }"
        type="number"
        :value="params[spec.key] ?? 0"
        :min="spec.min"
        :max="spec.max"
        :disabled="disabled"
        @input="onNumber(spec.key, ($event.target as HTMLInputElement).value, spec)"
      />
      <input
        v-else
        class="ct-check"
        type="checkbox"
        :checked="Boolean(params[spec.key])"
        :disabled="disabled"
        @change="params = { ...params, [spec.key]: ($event.target as HTMLInputElement).checked }"
      />
      <span v-if="spec.unit" class="unit">{{ spec.unit }}</span>
    </label>
  </div>
</template>

<style scoped>
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(132px, 1fr));
  gap: var(--space-3);
  margin-top: var(--space-4);
}

.field {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}

.label {
  flex: 0 0 62px;
  color: var(--color-text-subtle);
  font-size: var(--font-size-xs);
}

.field .ct-input {
  flex: 1;
  min-width: 0;
}

/* 超出建议区间只标色不拦截：用户有权这么设，但该知道代价。 */
.field .ct-input.warn {
  border-color: var(--color-warn);
  color: var(--color-warn);
}

/* 自适应徽标：小、克制，但一眼能看出这个值不是自己设的。点一下还原。 */
.badge {
  margin-left: var(--space-1);
  padding: 1px var(--space-2);
  border: 1px solid var(--color-info-border);
  border-radius: var(--radius-pill);
  background: var(--color-info-bg);
  color: var(--color-info);
  font-size: 10px;
  white-space: nowrap;
  cursor: pointer;
}

.unit {
  color: var(--color-text-subtle);
  font-size: var(--font-size-xs);
}
</style>
