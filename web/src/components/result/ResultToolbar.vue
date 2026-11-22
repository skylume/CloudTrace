<script setup lang="ts">
/**
 * 结果页工具栏：视图、搜索、分组、列设置，以及收在「更多」里的低频动作。
 *
 * 十几个控件等权排开时，每次都要用的搜索与视图切换会被淹掉。这里只留常用的
 * 一排，其余收进「更多」——找得到，但不占视线。
 *
 * 只连 store 与 emit，不自己发起测速：发起测速要按当前配置组装整份参数，那是
 * 页面的事，工具栏只表达「用户要测哪个范围」。
 */
import { computed, ref } from 'vue'

import ColumnSettings from './ColumnSettings.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import { t } from '@/i18n'
import { readPath } from '@/i18n/settingsSchema'
import { COLUMN_PRESETS, useFieldsStore, type ColumnPresetId } from '@/stores/fields'
import { useExportStore } from '@/stores/export'
import { useResultsStore } from '@/stores/results'
import { useSettingsStore } from '@/stores/settings'

const view = defineModel<'result' | 'speed'>('view', { required: true })

const emit = defineEmits<{
  (event: 'copy', count: number): void
  (event: 'export'): void
  (event: 'speed', scope: 'single' | 'region' | 'all'): void
  (event: 'resume'): void
}>()

const results = useResultsStore()
const fields = useFieldsStore()
const exporter = useExportStore()
const settings = useSettingsStore()

const selectedCount = computed(() => results.selected.size)

/** 「更多」默认收起：延迟上限、列预设、复制与按地区测速都不是每轮都改的。 */
const moreOpen = ref(false)

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

/** 丢包档位。存的是与记录同单位的小数，界面上按百分比显示。 */
const LOSS_STEPS = [0, 0.01, 0.05, 0.1]

function toggleLoss(step: number): void {
  results.maxLoss = results.maxLoss === step ? null : step
}

/**
 * 评分权重说明：三项都从配置读，不写死。
 *
 * 抖动项默认权重是 0（等于不参与），不说清楚的话，用户会以为分数里已经算了
 * 抖动，而实际排序只按速度与延迟。
 */
const weightNote = computed(() => {
  const values = settings.values as unknown as Record<string, unknown> | null
  if (!values) return ''
  const weight = (path: string, fallback: number): number => {
    const value = readPath(values, path)
    return typeof value === 'number' ? value : fallback
  }
  return t('result.weightNote', {
    speed: weight('speed.weight_speed', 1),
    latency: weight('speed.weight_latency', 1),
    jitter: weight('speed.weight_jitter', 0),
  })
})

/** 分地区配额只在完全测速时生效，设置里开着就得在这里说清楚。 */
const regionQuota = computed(() => {
  const values = settings.values as unknown as Record<string, unknown> | null
  if (!values) return 0
  const value = readPath(values, 'speed.per_region_topn')
  return typeof value === 'number' && value > 0 ? value : 0
})
</script>

<template>
  <div class="toolbar">
    <!-- 常用：看哪一份、找哪一条、怎么分组、显示哪些列。 -->
    <div class="group">
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
      <select v-model="results.groupBy" class="ct-input" :aria-label="t('result.group.none')">
        <option value="none">{{ t('result.group.none') }}</option>
        <option value="colo">{{ t('result.group.colo') }}</option>
        <option value="region">{{ t('result.group.region') }}</option>
        <option value="asn">{{ t('result.group.asn') }}</option>
      </select>
      <ColumnSettings />
    </div>

    <div class="group push">
      <!--
        窄屏下「测速选中」与「导出」在底部固定操作栏里已有，这里不再重复；
        宽屏没有底部栏，这两个就是主入口。
      -->
      <button
        type="button"
        class="ct-btn ct-btn--primary dup"
        :disabled="selectedCount === 0"
        @click="emit('speed', 'single')"
      >
        {{ t('result.speedSelected', { count: selectedCount }) }}
      </button>
      <button type="button" class="ct-btn dup" :disabled="exporter.pending" @click="emit('export')">
        {{ t('common.export') }}
      </button>
      <button
        type="button"
        class="ct-btn"
        :aria-expanded="moreOpen"
        aria-controls="ct-result-more"
        @click="moreOpen = !moreOpen"
      >
        {{ t('common.more') }}
      </button>
    </div>

    <div v-if="moreOpen" id="ct-result-more" class="more">
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
      <!--
        「仅显示达标」只在测速视图下出现：结果视图里还没有速度，勾了只会得到一张空表。
      -->
      <label v-if="view === 'speed'" class="filter">
        <input v-model="results.qualifiedOnly" type="checkbox" class="ct-check" />
        <span>{{ t('result.filter.qualified') }}</span>
      </label>

      <!-- 丢包上限：0 / 1% / 5% / 10%，再点一次取消。 -->
      <div class="loss-group" role="group" :aria-label="t('result.filter.loss')">
        <span class="ct-subtle">{{ t('result.filter.loss') }}</span>
        <button
          v-for="step in LOSS_STEPS"
          :key="step"
          type="button"
          class="chip"
          :class="{ on: results.maxLoss === step }"
          :aria-pressed="results.maxLoss === step"
          @click="toggleLoss(step)"
        >
          {{ step === 0 ? t('result.filter.lossNone') : `${Math.round(step * 100)}%` }}
        </button>
      </div>

      <SegmentedControl
        :segments="presetSegments"
        :model-value="fields.presetId"
        @update:model-value="fields.applyPreset($event as ColumnPresetId)"
      />

      <button type="button" class="ct-btn" @click="emit('copy', 3)">{{ t('result.copyTop') }}</button>
      <button type="button" class="ct-btn" @click="emit('copy', results.visible.length)">
        {{ t('result.copyAll') }}
      </button>

      <!--
        三种测速范围并排给出，各自的可用条件写在按钮的禁用状态上：
        没勾选就没法「测速选中」，没筛地区就没法「测速所选地区」。
        合成一个下拉会把「这次到底要测哪些」藏起来，而那是发起前唯一要确认的事。
      -->
      <div class="speed-group">
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

      <!--
        续测：拿上一轮的请求再扫一遍，跳过已经出结果的地址。整批重来既浪费
        时间，也会把刚挑出来的节点冲掉。
      -->
      <button type="button" class="ct-btn" :disabled="results.total === 0" @click="emit('resume')">
        {{ t('result.resume', { count: results.total }) }}
      </button>

      <p v-if="weightNote" class="note">{{ weightNote }}</p>
      <p v-if="regionQuota > 0" class="note">{{ t('result.regionQuota', { count: regionQuota }) }}</p>

      <button type="button" class="ct-btn" @click="results.resetView()">{{ t('result.resetView') }}</button>
    </div>
  </div>
</template>

<style scoped>
.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  /* 组间比组内松一档：两组各自成块，才不会看成一排等权的按钮。 */
  gap: var(--space-2) var(--space-3);
}

.group {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}

/* 动作组贴右：宽屏上这一行因此有左右两半的层次。 */
.push {
  margin-left: auto;
}

/* 「更多」展开后独占一行，与常用控件用虚线分开。 */
.more {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  flex-basis: 100%;
  gap: var(--space-2) var(--space-3);
  padding-top: var(--space-2);
  border-top: 1px dashed var(--color-border);
}

/*
 * 窄屏：底部固定操作栏已经给了「复制全部 / 导出 / 测速选中」，工具栏再摆一遍
 * 只会把表格往下顶四五行的距离。
 */
@media (max-width: 768px) {
  .push {
    margin-left: 0;
  }

  .dup {
    display: none;
  }
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

.loss-group {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  font-size: var(--font-size-sm);
}

.chip {
  padding: 2px var(--space-2);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-pill);
  background: var(--color-surface);
  color: var(--color-text-muted);
  font-size: var(--font-size-xs);
  cursor: pointer;
}

.chip:hover {
  border-color: var(--color-primary);
  color: var(--color-text);
}

.chip.on {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
  color: var(--color-primary-text);
}

/* 说明行独占一行：和控件挤在同一排会看起来像又一个可点的东西。 */
.note {
  flex-basis: 100%;
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--font-size-xs);
}

/* 三个测速入口要么都在这一行、要么整体挪到下一行，不拆散。 */
.speed-group {
  display: flex;
  flex-wrap: nowrap;
  gap: var(--space-2);
}
</style>
