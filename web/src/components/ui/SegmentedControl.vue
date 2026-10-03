<script setup lang="ts">
/**
 * 分段控件：档位三选一、结果/测速视图切换都用它。
 *
 * 选中态靠「抬起」表达——浅色下用投影，深色下用亮一档的表面加描边。
 * 与全站的分层手段保持一致，不另起一套选中语言。
 */
export interface Segment {
  value: string
  label: string
  disabled?: boolean
}

defineProps<{
  segments: Segment[]
  modelValue: string
  /** 无障碍标签，说明这组分段在选什么。 */
  label?: string
}>()

const emit = defineEmits<{ (event: 'update:modelValue', value: string): void }>()
</script>

<template>
  <div class="seg" role="tablist" :aria-label="label">
    <button
      v-for="segment in segments"
      :key="segment.value"
      type="button"
      role="tab"
      :aria-selected="modelValue === segment.value"
      :disabled="segment.disabled"
      :class="{ on: modelValue === segment.value }"
      @click="emit('update:modelValue', segment.value)"
    >
      {{ segment.label }}
    </button>
  </div>
</template>

<style scoped>
.seg {
  display: inline-flex;
  gap: 2px;
  padding: 3px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-surface-sunken);
}

.seg button {
  height: 26px;
  padding: 0 var(--space-4);
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-muted);
  font-size: var(--font-size-sm);
  white-space: nowrap;
  cursor: pointer;
}

.seg button:hover:not(:disabled):not(.on) {
  color: var(--color-text);
}

.seg button.on {
  background: var(--color-surface);
  color: var(--color-text);
  font-weight: 500;
  box-shadow: var(--elevation-1);
}

/* 深色下投影看不见，改用亮一档的表面 + 描边。 */
:root[data-theme='dark'] .seg button.on {
  background: var(--color-surface-raised);
  border: 1px solid var(--color-border-highlight);
  box-shadow: none;
}

.seg button:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
</style>
