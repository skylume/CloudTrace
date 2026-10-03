<script setup lang="ts">
/**
 * 档位与参数。
 *
 * 关键决定：**选档位与调参数是同一个界面**。档位只是「批量填一组值」，
 * 填完立刻可以改——如果参数藏在另一个「高级模式」里，用户会以为档位是
 * 一道关卡，而规格明确要求它只是快捷填充。
 *
 * 参数的中文标签、范围、说明都来自 i18n 映射表，组件里不硬编码。
 */
import { computed, ref } from 'vue'

import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import { t } from '@/i18n'
import { CUSTOM_PRESET, SCAN_PARAMS, SCAN_PRESETS, matchPreset } from '@/i18n/params'

const params = defineModel<Record<string, number | boolean>>('params', { required: true })
const presetId = defineModel<string>('presetId', { required: true })

defineProps<{ disabled?: boolean }>()

const expanded = ref(false)

const segments = computed(() => [
  ...SCAN_PRESETS.map((preset) => ({ value: preset.id, label: t(preset.labelKey as never) })),
  { value: CUSTOM_PRESET, label: t('preset.custom') },
])

/** 摘要里显示的参数；展开后显示全部。 */
const visibleParams = computed(() =>
  expanded.value ? SCAN_PARAMS : SCAN_PARAMS.filter((spec) => spec.summary),
)

/** 偏离档位时的提示：告诉用户「你已经不在原来的档位上了」。 */
const deviatedFrom = computed(() => {
  if (presetId.value === CUSTOM_PRESET) return ''
  const current = matchPreset(params.value)
  if (current !== CUSTOM_PRESET) return ''
  const source = SCAN_PRESETS.find((preset) => preset.id === presetId.value)
  return source ? t('preset.deviated', { name: t(source.labelKey as never) }) : ''
})

function applyPreset(id: string): void {
  presetId.value = id
  const preset = SCAN_PRESETS.find((item) => item.id === id)
  if (!preset) return
  params.value = { ...params.value, ...preset.values }
}

/**
 * 改一个参数。
 *
 * 改完重新判定档位：改回原值就该自动回到那个档位，而不是一直挂着「自定义」。
 */
function updateParam(key: string, value: number | boolean): void {
  const next = { ...params.value, [key]: value }
  params.value = next
  presetId.value = matchPreset(next)
}

function onNumber(key: string, raw: string, spec: { min?: number; max?: number }): void {
  const value = Number(raw)
  if (!Number.isFinite(value)) return
  const clamped = Math.min(Math.max(value, spec.min ?? 0), spec.max ?? Number.MAX_SAFE_INTEGER)
  updateParam(key, clamped)
}
</script>

<template>
  <section class="ct-card">
    <h2 class="ct-card-title">
      <span>{{ t('preset.summary') }}</span>
      <span v-if="deviatedFrom" class="deviated">{{ deviatedFrom }}</span>
      <span class="spacer" />
      <button type="button" class="ct-link" @click="expanded = !expanded">
        {{ expanded ? t('preset.collapse') : t('preset.all') }}
      </button>
    </h2>

    <SegmentedControl
      :segments="segments"
      :model-value="presetId"
      :label="t('preset.summary')"
      @update:model-value="applyPreset"
    />

    <div class="grid">
      <label v-for="spec in visibleParams" :key="spec.key" class="field">
        <span class="label" :title="t(spec.hintKey as never)">{{ t(spec.labelKey as never) }}</span>
        <input
          v-if="spec.kind === 'int'"
          class="ct-input tnum"
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
          @change="updateParam(spec.key, ($event.target as HTMLInputElement).checked)"
        />
        <span v-if="spec.unit" class="unit">{{ spec.unit }}</span>
      </label>
    </div>
  </section>
</template>

<style scoped>
.spacer {
  flex: 1;
}

.deviated {
  color: var(--color-warn);
  font-size: var(--font-size-xs);
  font-weight: 400;
}

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

.unit {
  color: var(--color-text-subtle);
  font-size: var(--font-size-xs);
}
</style>
