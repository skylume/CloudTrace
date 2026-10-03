<script setup lang="ts">
/**
 * 结果卡片列表：窄屏下替代表格。
 *
 * 手机上不该出现横向滚动的表格——列挤到看不清，横向滑动又容易误触。改成
 * 卡片：每张常显 6 个关键字段（地址 / 地区 / 延迟 / 丢包 / 下载 / 评分），
 * 点开复用桌面端的详情组件看到全部字段。
 *
 * **复用详情组件而不是另做一套**：两套渲染迟早会不一致，而「手机上看到的
 * 和电脑上不一样」是最难排查的一类问题。
 */
import { ref } from 'vue'

import RecordDetail from './RecordDetail.vue'
import SignalBar from '@/components/ui/SignalBar.vue'
import { t } from '@/i18n'
import type { IPRecord } from '@/api/types'
import { useFieldsStore } from '@/stores/fields'
import { useUIStore } from '@/stores/ui'
import { recordKey, useResultsStore } from '@/stores/results'
import { formatField } from '@/utils/recordFormat'
import { formatLoss, formatSpeed } from '@/utils/latency'

const props = defineProps<{
  records: IPRecord[]
}>()

const results = useResultsStore()
const fields = useFieldsStore()
const ui = useUIStore()
const expanded = ref<string[]>([])

/** 左滑阈值：小于这个位移才算误触，不做任何事。 */
const SWIPE_THRESHOLD = 60

/** 当前被滑动的卡片，用于把它往左推一点给出反馈。 */
const swiping = ref<{ key: string; dx: number } | null>(null)
let touchStartX = 0

function onTouchStart(record: IPRecord, event: TouchEvent): void {
  touchStartX = event.touches[0]?.clientX ?? 0
  swiping.value = { key: recordKey(record), dx: 0 }
}

function onTouchMove(record: IPRecord, event: TouchEvent): void {
  if (!swiping.value || swiping.value.key !== recordKey(record)) return
  const current = event.touches[0]?.clientX ?? 0
  // 只认左滑：右滑在这里没有语义，跟着动会让卡片看起来能拖动。
  const dx = Math.min(0, current - touchStartX)
  swiping.value = { key: recordKey(record), dx }
}

function onTouchEnd(record: IPRecord): void {
  const dx = swiping.value?.dx ?? 0
  swiping.value = null
  if (dx > -SWIPE_THRESHOLD) return
  void copy(record)
  ui.pushToast({ kind: 'ok', message: t('common.copied') })
}

function toggle(record: IPRecord): void {
  const key = recordKey(record)
  expanded.value = expanded.value.includes(key)
    ? expanded.value.filter((item) => item !== key)
    : [...expanded.value, key]
}

/** 复制一行 ip:port。手机上没有双击，用显式按钮。 */
async function copy(record: IPRecord): Promise<void> {
  try {
    await navigator.clipboard.writeText(`${record.ip}:${record.port}`)
  } catch {
    /* 剪贴板不可用时交给工具栏的「复制全部」 */
  }
}
</script>

<template>
  <ul class="cards">
    <li v-for="record in props.records" :key="recordKey(record)" class="card">
      <div
        class="head"
        :style="{ transform: `translateX(${swiping?.key === recordKey(record) ? swiping.dx : 0}px)` }"
        @click="toggle(record)"
        @touchstart.passive="onTouchStart(record, $event)"
        @touchmove.passive="onTouchMove(record, $event)"
        @touchend="onTouchEnd(record)"
      >
        <input
          type="checkbox"
          class="ct-check"
          :checked="results.selected.has(recordKey(record))"
          :aria-label="record.ip"
          @click.stop
          @change="results.toggleSelect(recordKey(record))"
        />
        <span class="ip ct-mono">{{ record.ip }}:{{ record.port }}</span>
        <span class="spacer" />
        <span class="ct-subtle">{{ expanded.includes(recordKey(record)) ? '▾' : '▸' }}</span>
      </div>

      <dl class="grid">
        <div>
          <dt>{{ t('result.stat.regions') }}</dt>
          <dd>{{ record.region_name || record.colo || '—' }}</dd>
        </div>
        <div>
          <dt>{{ fields.byKey.get('latency')?.label ?? 'latency' }}</dt>
          <dd><SignalBar :latency="record.latency" /></dd>
        </div>
        <div>
          <dt>{{ fields.byKey.get('loss')?.label ?? 'loss' }}</dt>
          <dd class="tnum">{{ formatLoss(record.loss, record.sent) }}</dd>
        </div>
        <div>
          <dt>{{ fields.byKey.get('speed_mbps')?.label ?? 'speed' }}</dt>
          <dd class="tnum">{{ formatSpeed(record.speed_mbps) }}</dd>
        </div>
        <div>
          <dt>{{ fields.byKey.get('score')?.label ?? 'score' }}</dt>
          <dd class="tnum">{{ formatField('score', record.score, record) }}</dd>
        </div>
      </dl>

      <div class="actions">
        <button type="button" class="ct-btn" @click="copy(record)">{{ t('common.copy') }}</button>
        <button type="button" class="ct-btn" @click="toggle(record)">
          {{ expanded.includes(recordKey(record)) ? t('result.collapse') : t('result.expand') }}
        </button>
      </div>

      <RecordDetail v-if="expanded.includes(recordKey(record))" :record="record" :fields="fields.fields" />
    </li>
  </ul>
</template>

<style scoped>
.cards {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
}

.card {
  border: 1px solid var(--color-border);
  border-top-color: var(--color-border-highlight);
  border-radius: var(--radius-lg);
  background: var(--color-surface);
  box-shadow: var(--elevation-1);
  overflow: hidden;
}

.head {
  display: flex;
  /* 左滑时跟着手指移动一点，让「可以滑」这件事被发现。 */
  transition: transform var(--duration-fast) var(--ease);
  touch-action: pan-y;
  align-items: center;
  gap: var(--space-3);
  /* 整行可点，触摸目标远大于 44px。 */
  min-height: 48px;
  padding: 0 var(--space-4);
  cursor: pointer;
}

.ip {
  font-size: var(--font-size-sm);
}

.grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2) var(--space-3);
  margin: 0;
  padding: 0 var(--space-4) var(--space-3);
}

.grid dt {
  color: var(--color-text-subtle);
  font-size: var(--font-size-xs);
}

.grid dd {
  margin: 2px 0 0;
  font-size: var(--font-size-sm);
}

.actions {
  display: flex;
  gap: var(--space-2);
  padding: 0 var(--space-4) var(--space-3);
}

.actions .ct-btn {
  min-height: 40px;
  flex: 1;
}

.spacer {
  flex: 1;
}
</style>
