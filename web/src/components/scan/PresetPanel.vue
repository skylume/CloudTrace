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
import { computed } from 'vue'

import Banner from '@/components/ui/Banner.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import { t } from '@/i18n'
import { CUSTOM_PRESET, SCAN_PARAMS, SCAN_PRESETS, matchPreset } from '@/i18n/params'
import { useAdaptiveStore, type AdaptiveNotice } from '@/stores/adaptive'
import { useUIStore } from '@/stores/ui'

const params = defineModel<Record<string, number | boolean>>('params', { required: true })
const presetId = defineModel<string>('presetId', { required: true })

defineProps<{ disabled?: boolean }>()

const ui = useUIStore()
const adaptive = useAdaptiveStore()

/**
 * 建议的整句话。
 *
 * 在脚本里拼好而不是写进模板：模板里的模板字面量容易被工具链吞掉（这个仓库
 * 已经被坑过三次），而且拼装逻辑放在这里也更好读。
 */
function suggestionText(item: AdaptiveNotice): string {
  return adaptive.reasonText(item.reason) + '　' + item.key + ' ' + item.from + ' → ' + item.to
}

/**
 * 是否展开全部参数。
 *
 * 由 `ui.density` 决定而不是组件自己的状态：披露是「首次默认」，不是每次的
 * 关卡——用户展开过一次，下次进来就该直接是展开的，不该让他再点一次。
 */
const expanded = computed(() => ui.showAdvanced)

/**
 * 展开 / 收起。
 *
 * 用户的最近一次选择说了算：展开记成「高级」，收起记成「简单」。`auto` 只在
 * 用户还没表过态时起作用（默认简单）。这比「展开过就再也收不回去」更符合
 * 直觉，而想回到简单还有 Ctrl+Shift+A。
 */
function toggleExpanded(): void {
  ui.setDensity(expanded.value ? 'simple' : 'advanced')
}

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
      <button type="button" class="ct-link" @click="toggleExpanded">
        {{ expanded ? t('preset.collapse') : t('preset.all') }}
      </button>
    </h2>

    <SegmentedControl
      :segments="segments"
      :model-value="presetId"
      :label="t('preset.summary')"
      @update:model-value="applyPreset"
    />

    <!-- 建议：值没有变，等用户自己决定。绝不替他点。 -->
    <Banner
      v-for="item in adaptive.visibleSuggestions"
      :key="item.key"
      tone="info"
      :message="suggestionText(item)"
      :action-label="t('adaptive.accept')"
      @action="adaptive.accept(item)"
      @close="adaptive.dismiss(item.key)"
    />

    <p v-if="deviatedFrom" class="notice ct-subtle">{{ t('preset.appliesNextRun') }}</p>

    <div class="grid">
      <label v-for="spec in visibleParams" :key="spec.key" class="field">
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

.notice {
  margin: var(--space-2) 0 0;
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

/* 超出建议区间只标色不拦截：用户有权这么设，但该知道代价。 */
.field .ct-input.warn {
  border-color: var(--color-warn);
  color: var(--color-warn);
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
