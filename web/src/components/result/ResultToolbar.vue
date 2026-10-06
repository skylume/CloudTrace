<script setup lang="ts">
/**
 * 结果页工具栏：视图切换、筛选、呈现方式、动作。
 *
 * 单独一个组件而不是摊在页面模板里：这一行有十几个控件，摊在页面里会把页面的
 * 结构淹没掉——页面该回答的是「这一页有哪几块」，不是「这一行有哪些按钮」。
 *
 * 只连 store 与 emit，不自己发起测速：发起测速要按当前配置组装整份参数，那是
 * 页面的事，工具栏只表达「用户要测哪个范围」。
 */
import { computed } from 'vue'

import ColumnSettings from './ColumnSettings.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import { t } from '@/i18n'
import { COLUMN_PRESETS, useFieldsStore, type ColumnPresetId } from '@/stores/fields'
import { useExportStore } from '@/stores/export'
import { useResultsStore } from '@/stores/results'

const view = defineModel<'result' | 'speed'>('view', { required: true })

const emit = defineEmits<{
  (event: 'copy', count: number): void
  (event: 'export'): void
  (event: 'speed', scope: 'single' | 'region' | 'all'): void
}>()

const results = useResultsStore()
const fields = useFieldsStore()
const exporter = useExportStore()

const selectedCount = computed(() => results.selected.size)

const presetSegments = computed(() =>
  COLUMN_PRESETS.map((preset) => ({ value: preset.id, label: t(preset.labelKey as never) })),
)

/**
 * 延迟上限的输入值。
 *
 * 存储侧用 `null` 表示「不限」，而输入框清空时给的是空串——两边直接绑会在
 * 「清空 = 变成 0」上出错，而 0 会滤掉所有节点。
 */
const latencyLimit = computed({
  get: () => (results.maxLatency === null ? '' : String(results.maxLatency)),
  set: (value: string) => {
    const parsed = Number.parseInt(value, 10)
    results.maxLatency = Number.isFinite(parsed) && parsed > 0 ? parsed : null
  },
})
</script>

<template>
  <div class="toolbar">
    <SegmentedControl
      v-model="view"
      :segments="[
        { value: 'result', label: t('result.view.result') },
        { value: 'speed', label: t('result.view.speed') },
      ]"
    />
    <input
      id="ct-result-search"
      v-model="results.keyword"
      class="ct-input search"
      type="search"
      :placeholder="t('common.search')"
    />
    <label class="filter">
      <span class="ct-subtle">{{ t('result.filter.latency') }}</span>
      <input
        v-model="latencyLimit"
        class="ct-input filter-input"
        type="number"
        min="0"
        step="10"
        inputmode="numeric"
        :placeholder="t('result.filter.unlimited')"
      />
    </label>
    <span class="spacer" />
    <SegmentedControl
      :segments="presetSegments"
      :model-value="fields.presetId"
      @update:model-value="fields.applyPreset($event as ColumnPresetId)"
    />
    <ColumnSettings />
    <select v-model="results.groupBy" class="ct-input" :aria-label="t('result.group.none')">
      <option value="none">{{ t('result.group.none') }}</option>
      <option value="colo">{{ t('result.group.colo') }}</option>
      <option value="region">{{ t('result.group.region') }}</option>
      <option value="asn">{{ t('result.group.asn') }}</option>
    </select>
    <button type="button" class="ct-btn" @click="emit('copy', 3)">{{ t('result.copyTop') }}</button>
    <button type="button" class="ct-btn" @click="emit('copy', results.visible.length)">
      {{ t('result.copyAll') }}
    </button>
    <button type="button" class="ct-btn" :disabled="exporter.pending" @click="emit('export')">
      {{ t('common.export') }}
    </button>
    <!--
      三种测速范围并排给出，各自的可用条件写在按钮的禁用状态上：
      没勾选就没法「测速选中」，没筛地区就没法「测速所选地区」。
      合成一个下拉会把「这次到底要测哪些」藏起来，而那是发起前唯一要确认的事。

      三个包在一个不换行的组里：工具栏本身会换行，拆散的话窄屏上会出现
      「一个在上一行、两个在下一行」这种断法。
    -->
    <div class="speed-group">
      <button type="button" class="ct-btn ct-btn--primary" :disabled="selectedCount === 0" @click="emit('speed', 'single')">
        {{ t('result.speedSelected', { count: selectedCount }) }}
      </button>
      <button
        type="button"
        class="ct-btn"
        :disabled="results.regionFilter.length === 0 || results.visible.length === 0"
        @click="emit('speed', 'region')"
      >
        {{ t('result.speedRegion') }}
      </button>
      <button type="button" class="ct-btn" :disabled="results.total === 0" @click="emit('speed', 'all')">
        {{ t('result.speedAll') }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.spacer {
  flex: 1;
}

.search {
  width: 180px;
}

.filter {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  font-size: var(--font-size-sm);
}

.filter-input {
  width: 84px;
}

/* 三个测速入口要么都在这一行、要么整体挪到下一行，不拆散。 */
.speed-group {
  display: flex;
  flex-wrap: nowrap;
  gap: var(--space-2);
}
</style>
