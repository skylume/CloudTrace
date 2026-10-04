<script setup lang="ts">
/**
 * 档位选择。
 *
 * 单独成一个组件，是为了让它能站在首屏的动作行里——「选档位」和「开始扫描」
 * 是同一个决定的两半：选完就点。参数明细、联动提示、档位管理都在别处。
 */
import { computed, watch } from 'vue'

import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import { t } from '@/i18n'
import { CUSTOM_PRESET, matchPreset } from '@/i18n/params'
import { usePresetsStore } from '@/stores/presets'

const params = defineModel<Record<string, number | boolean>>('params', { required: true })
const presetId = defineModel<string>('presetId', { required: true })

const presets = usePresetsStore()

/**
 * 下拉的选项。
 *
 * 列表来自后端，顺序也由后端定（内置在前、各自按 Order 排）。前端不重排：
 * 两处都排序，用户看到的顺序与后端认定的顺序迟早会不一样。
 */
const segments = computed(() => [
  ...presets.list.map((preset) => ({ value: preset.id, label: preset.name })),
  { value: CUSTOM_PRESET, label: t('preset.custom') },
])

/** 偏离档位时的提示：告诉用户「你已经不在原来的档位上了」。 */
const deviatedFrom = computed(() => {
  if (presetId.value === CUSTOM_PRESET) return ''
  if (matchPreset(params.value, presets.list) !== CUSTOM_PRESET) return ''
  const source = presets.byID(presetId.value)
  return source ? t('preset.deviated', { name: source.name }) : ''
})

/**
 * 参数一变就重新判定档位。
 *
 * 档位由参数**推**出来，而不是另外记一个状态：改回原值就该自动回到那个档位，
 * 而不是一直挂着「自定义」。换档位时反过来——服务端写完配置广播回来，参数变
 * 了，这里跟着判定成新档位。
 */
watch(
  params,
  () => {
    presetId.value = matchPreset(params.value, presets.list)
  },
  { deep: true },
)

/**
 * 换档位。
 *
 * 只发命令，不在本地填值：服务端写完配置会广播回来，面板跟着填。两边各写一次
 * 看着更快，但两次写的来源标记不一样（档位填的标 preset、面板写回的标 user），
 * 谁后到谁说了算——结果是自适应能不能动这组值变得不确定。
 */
function apply(id: string): void {
  presetId.value = id
  if (id === CUSTOM_PRESET) return
  presets.use(id)
}
</script>

<template>
  <div class="picker">
    <SegmentedControl
      :segments="segments"
      :model-value="presetId"
      :label="t('preset.summary')"
      @update:model-value="apply"
    />
    <span v-if="deviatedFrom" class="deviated" :title="t('preset.appliesNextRun')">{{ deviatedFrom }}</span>
  </div>
</template>

<style scoped>
.picker {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
  min-width: 0;
}

.deviated {
  color: var(--color-warn);
  font-size: var(--font-size-xs);
  cursor: help;
}
</style>
