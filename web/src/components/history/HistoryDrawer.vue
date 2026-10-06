<script setup lang="ts">
/**
 * 历史详情抽屉。
 *
 * 只在用户明确要看某一份时才读记录文件（`history/get`）——列表页走的是索引，
 * 一条记录都不打开，这是历史体系最要紧的性能约束。
 *
 * 参数快照按「配置项 → 标签」的表翻译。翻译不出来的键原样显示：后端加了新
 * 参数而前端还没跟上时，至少能看到它叫什么，而不是整块消失。
 */
import { computed } from 'vue'

import { t } from '@/i18n'
import { SCAN_PARAMS, PARAM_WIRE_KEYS } from '@/i18n/params'
import type { HistoryRecord, IPRecord } from '@/api/types'
import { formatLatency, formatSpeed } from '@/utils/latency'
import { formatAbsolute } from '@/utils/timeText'

const props = defineProps<{ record: HistoryRecord; timeFormat: 'local' | 'utc' }>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'load', id: string): void
  (event: 'rerun', record: HistoryRecord): void
}>()

/** 参数快照里的键是后端的下划线名，标签表用的是界面名，这里对一次。 */
const LABELS = new Map<string, string>(
  SCAN_PARAMS.map((spec) => [PARAM_WIRE_KEYS[spec.key] ?? spec.key, spec.labelKey]),
)

/** 快照里不是扫描参数的几项，单独给标签。 */
const EXTRA_LABELS: Record<string, string> = {
  ip_version: 'param.scan.ip_version',
  source_mode: 'scan.sourceLabel',
}

const snapshot = computed(() => {
  const raw = props.record.params
  if (!raw || typeof raw !== 'object') return []
  return Object.entries(raw as Record<string, unknown>)
    .filter(([, value]) => value !== null && value !== undefined && value !== '')
    .map(([key, value]) => ({
      key,
      label: labelOf(key),
      value: renderValue(value),
    }))
})

function labelOf(key: string): string {
  const labelKey = LABELS.get(key) ?? EXTRA_LABELS[key]
  if (!labelKey) return key
  const translated = t(labelKey as never)
  return translated === labelKey ? key : translated
}

function renderValue(value: unknown): string {
  if (typeof value === 'boolean') return value ? t('opt.true') : t('opt.false')
  if (Array.isArray(value)) return value.length > 0 ? value.join('、') : t('common.none')
  return String(value)
}

/** 只列关键几列：抽屉是「看一眼」的地方，全字段在结果页的行内展开里。 */
function latencyOf(record: IPRecord): string {
  return `${formatLatency(record.latency)}ms`
}
</script>

<template>
  <div class="scrim" @click="emit('close')" />
  <aside class="drawer" role="dialog" aria-modal="true">
    <header class="head">
      <div>
        <h2 class="title">{{ t('history.detailTitle') }}</h2>
        <p class="ct-subtle meta">
          {{ formatAbsolute(record.created_at, props.timeFormat) }}
          · {{ t(`task.phase.${record.type}`) }}
          <template v-if="record.preset"> · {{ t('task.preset') }} {{ record.preset }}</template>
          <template v-if="record.duration_s"> · {{ record.duration_s }}s</template>
        </p>
      </div>
      <span class="spacer" />
      <button type="button" class="ct-link" @click="emit('close')">{{ t('common.close') }}</button>
    </header>

    <div class="stats">
      <div><span class="ct-subtle">{{ t('result.stat.total') }}</span><b class="tnum">{{ record.count }}</b></div>
      <div><span class="ct-subtle">{{ t('result.stat.minLatency') }}</span><b class="tnum">{{ formatLatency(record.summary.min_latency) }}ms</b></div>
      <div><span class="ct-subtle">{{ t('result.stat.bestSpeed') }}</span><b class="tnum">{{ formatSpeed(record.summary.best_speed) }}MB/s</b></div>
    </div>

    <section class="block">
      <h3 class="block-title">{{ t('history.snapshot') }}</h3>
      <dl class="snapshot">
        <div v-for="item in snapshot" :key="item.key">
          <dt>{{ item.label }}</dt>
          <dd class="ct-mono">{{ item.value }}</dd>
        </div>
      </dl>
    </section>

    <section class="block">
      <h3 class="block-title">{{ t('history.results') }}</h3>
      <div class="table-wrap">
        <table class="table">
          <thead>
            <tr>
              <th>{{ t('result.stat.usable') }}</th>
              <th>{{ t('result.filter.region') }}</th>
              <th class="num">{{ t('result.stat.minLatency') }}</th>
              <th class="num">{{ t('result.stat.bestSpeed') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in record.results.slice(0, 100)" :key="`${item.ip}:${item.port}`">
              <td class="ct-mono">{{ item.ip }}:{{ item.port }}</td>
              <td>{{ item.region_name || item.colo || '—' }}</td>
              <td class="num tnum">{{ latencyOf(item) }}</td>
              <td class="num tnum">{{ formatSpeed(item.speed_mbps) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="record.results.length > 100" class="ct-subtle more">
        {{ t('history.moreResults', { count: record.results.length - 100 }) }}
      </p>
    </section>

    <footer class="foot">
      <button type="button" class="ct-btn" @click="emit('rerun', record)">{{ t('history.rerun') }}</button>
      <button type="button" class="ct-btn ct-btn--primary" @click="emit('load', record.id)">
        {{ t('history.loadToResult') }}
      </button>
    </footer>
  </aside>
</template>

<style scoped>
.scrim {
  position: fixed;
  inset: 0;
  z-index: var(--z-dialog);
  background: rgb(0 0 0 / 32%);
}

.drawer {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  z-index: calc(var(--z-dialog) + 1);
  width: min(560px, 92vw);
  padding: var(--space-5);
  overflow-y: auto;
  background: var(--color-surface);
  border-left: 1px solid var(--color-border);
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.head {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
}

.title {
  margin: 0;
  font-size: var(--font-size-lg);
}

.meta {
  margin: 2px 0 0;
  font-size: var(--font-size-xs);
}

.spacer {
  flex: 1;
}

.stats {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  gap: var(--space-3);
}

.stats div {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: var(--font-size-sm);
}

.block-title {
  margin: 0 0 var(--space-2);
  font-size: var(--font-size-sm);
  color: var(--color-text-muted);
  font-weight: 500;
}

.snapshot {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: var(--space-2) var(--space-4);
  margin: 0;
}

.snapshot dt {
  color: var(--color-text-subtle);
  font-size: var(--font-size-xs);
}

.snapshot dd {
  margin: 0;
  font-size: var(--font-size-sm);
  overflow-wrap: anywhere;
}

.table-wrap {
  max-height: 320px;
  overflow: auto;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
}

.table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--font-size-sm);
}

.table th,
.table td {
  padding: 0 var(--space-3);
  height: var(--row-height);
  text-align: left;
  white-space: nowrap;
  border-bottom: 1px solid var(--color-border);
}

.table th {
  position: sticky;
  top: 0;
  background: var(--color-surface-sunken);
  color: var(--color-text-subtle);
  font-size: var(--font-size-xs);
  font-weight: 500;
}

.num {
  text-align: right;
}

.more {
  margin: var(--space-2) 0 0;
  font-size: var(--font-size-xs);
}

.foot {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  margin-top: auto;
  padding-top: var(--space-3);
  border-top: 1px solid var(--color-border);
}
</style>
