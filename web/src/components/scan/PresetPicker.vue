<script setup lang="ts">
/**
 * 档位选择。
 *
 * 单独成一个组件，是为了让它能站在首屏的动作行里——「选档位」和「开始扫描」
 * 是同一个决定的两半：选完就点。参数明细、联动提示、档位管理都在别处。
 */
import { computed, watch } from 'vue'

import { t } from '@/i18n'
import { CUSTOM_PRESET, matchPreset } from '@/i18n/params'
import { usePresetsStore } from '@/stores/presets'

const params = defineModel<Record<string, number | boolean>>('params', { required: true })
const presetId = defineModel<string>('presetId', { required: true })

const presets = usePresetsStore()

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

function onPick(event: Event): void {
  apply((event.target as HTMLSelectElement).value)
}
</script>

<template>
  <div class="picker">
    <!--
      用原生下拉而不是一排胶囊：内置三档加自定义档位会越来越长，一排胶囊在
      窄屏上会换行成两三行。分组也让「内置」与「我的」一眼分得开。
    -->
    <select class="ct-input preset-select" :value="presetId" :aria-label="t('preset.summary')" @change="onPick">
      <optgroup :label="t('preset.builtinGroup')">
        <option v-for="preset in presets.builtin" :key="preset.id" :value="preset.id">{{ preset.name }}</option>
      </optgroup>
      <optgroup v-if="presets.custom.length > 0" :label="t('preset.mine')">
        <option v-for="preset in presets.custom" :key="preset.id" :value="preset.id">{{ preset.name }}</option>
      </optgroup>
      <option :value="CUSTOM_PRESET">{{ t('preset.custom') }}</option>
    </select>
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

.preset-select {
  min-width: 160px;
  max-width: 240px;
}

.deviated {
  color: var(--color-warn);
  font-size: var(--font-size-xs);
  cursor: help;
}
</style>
