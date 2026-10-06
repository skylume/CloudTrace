<script setup lang="ts">
/**
 * 结果页：让用户 3 秒内看懂结果质量，1 次点击取走想要的 IP。
 *
 * 只做编排。表格、详情、统计都在子组件里——页面模板短，结构才会被逼着拆开。
 */
import { CMD } from '@/api/protocol'
import { computed, onMounted, ref, watch } from 'vue'

import { sendCommand } from '@/api/client'
import DataTable from '@/components/result/DataTable.vue'
import MobileActionBar from '@/components/result/MobileActionBar.vue'
import RecordCardList from '@/components/result/RecordCardList.vue'
import RegionChips from '@/components/result/RegionChips.vue'
import ResultToolbar from '@/components/result/ResultToolbar.vue'
import Banner from '@/components/ui/Banner.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Funnel, { type FunnelStep } from '@/components/ui/Funnel.vue'
import Pager from '@/components/ui/Pager.vue'
import { t } from '@/i18n'
import type { IPRecord } from '@/api/types'
import { useFieldsStore } from '@/stores/fields'
import { useExportStore } from '@/stores/export'
import { useHistoryStore } from '@/stores/history'
import { useResultsStore } from '@/stores/results'
import { useSettingsStore } from '@/stores/settings'
import { useSpeedStore } from '@/stores/speed'
import { useTaskStore } from '@/stores/task'
import { useUIStore } from '@/stores/ui'
import { formatLatency, formatSpeed } from '@/utils/latency'
import { buildSpeedParams } from '@/utils/speedParams'
import { useNarrow } from '@/utils/useMediaQuery'

const results = useResultsStore()
const fields = useFieldsStore()
const task = useTaskStore()
const speed = useSpeedStore()
const settings = useSettingsStore()
const exporter = useExportStore()
const history = useHistoryStore()

/**
 * 下拉刷新。
 *
 * 「刷新」在这里有明确语义，不是走个过场：
 *   - 结果来自某份历史 -> 重新读那一份（标签与备注可能被别的窗口改过）
 *   - 结果是本次扫描出来的 -> 从服务端拉「最新一份」，把另一个窗口或上一次
 *     运行的结果取回来
 *
 * 只在页面滚到顶部时才触发：列表已经往下翻过时再拉，用户想滚回顶部，而不是刷新。
 */
const pullDistance = ref(0)
const PULL_THRESHOLD = 70
let pullStartY = 0
let pulling = false

function onPullStart(event: TouchEvent): void {
  if (window.scrollY > 0) return
  pullStartY = event.touches[0]?.clientY ?? 0
  pulling = true
}

function onPullMove(event: TouchEvent): void {
  if (!pulling) return
  const current = event.touches[0]?.clientY ?? 0
  // 只认下拉；上滑是正常滚动，跟着算会让页面卡住。
  pullDistance.value = Math.max(0, current - pullStartY)
}

async function onPullEnd(): Promise<void> {
  if (!pulling) return
  pulling = false
  const distance = pullDistance.value
  pullDistance.value = 0
  if (distance < PULL_THRESHOLD) return
  await refreshResults()
}

async function refreshResults(): Promise<void> {
  if (results.sourceId !== '') {
    history.load(results.sourceId, { count: results.total })
    return
  }

  try {
    const response = await fetch('/latest.json', { credentials: 'same-origin' })
    if (!response.ok) {
      // 没跑过任务时是 404，这不是故障，只是没有可刷新的东西。
      ui.pushToast({ kind: 'warn', message: t('result.empty') })
      return
    }
    results.replaceAll((await response.json()) as IPRecord[])
    ui.pushToast({ kind: 'ok', message: t('result.refreshed') })
  } catch {
    ui.pushToast({ kind: 'bad', message: t('error.E_NETWORK') })
  }
}
const ui = useUIStore()

const narrow = useNarrow()
/**
 * 视图状态放在 ui store：命令面板的「完全测速」要能直接把用户带到测速视图。
 */
const view = computed({
  get: () => ui.resultView,
  set: (next: 'result' | 'speed') => (ui.resultView = next),
})
const expanded = ref<string[]>([])

/**
 * 切视图时换排序维度。
 *
 * 两个视图回答的是不同的问题：结果视图问「哪个延迟低」，测速视图问「哪个真的
 * 快」。切到测速就按评分降序——没测过的节点没有评分，会按「无值恒排最后」沉到
 * 后面，于是「哪些测过了、测出来怎么样」一眼就能看到，不必自己一页页翻。
 */
watch(view, (next) => {
  if (next === 'speed') {
    results.sortKey = 'score'
    results.sortDesc = true
    return
  }
  results.sortKey = 'latency'
  results.sortDesc = false
})

onMounted(() => void fields.load())

/** 统计卡：结果视图看延迟，测速视图看速度——同一批数据，两个关注点。 */
const stats = computed(() => {
  const summary = results.stats
  if (view.value === 'speed') {
    return [
      { label: t('result.stat.bestSpeed'), value: formatSpeed(summary.best_speed), unit: 'MB/s', tone: '' },
      { label: t('result.stat.avgSpeed'), value: formatSpeed(summary.avg_speed), unit: 'MB/s', tone: '' },
      { label: t('result.stat.qualified'), value: String(summary.qualified), unit: '', tone: '' },
      { label: t('result.stat.total'), value: String(summary.total), unit: '', tone: '' },
    ]
  }
  return [
    { label: t('result.stat.usable'), value: String(summary.total), unit: '', tone: '' },
    { label: t('result.stat.regions'), value: String(Object.keys(summary.region_dist).length), unit: '', tone: '' },
    { label: t('result.stat.minLatency'), value: formatLatency(summary.min_latency), unit: 'ms', tone: 'ok' },
    { label: t('result.stat.avgLatency'), value: formatLatency(summary.avg_latency), unit: 'ms', tone: '' },
  ]
})

const showEmpty = computed(() => results.total === 0 && !task.running)
const selectedCount = computed(() => results.selected.size)

/** 漏斗条：与扫描页右栏同一份数据，这里用紧凑版。 */
const funnelSteps = computed<FunnelStep[]>(() => [
  { label: t('funnel.generated'), value: task.funnel.generated },
  { label: t('funnel.latencyOk'), value: task.funnel.latency_ok },
  { label: t('funnel.regionOk'), value: task.funnel.region_ok },
  { label: t('funnel.usable'), value: task.funnel.usable, tone: 'ok' },
])

/** 有本次任务的过程数据、且看的不是历史那一份，漏斗才有意义。 */
const showFunnel = computed(() => results.sourceId === '' && task.funnel.generated > 0)

/**
 * 清空当前结果。
 *
 * 不弹确认框，走 Toast 撤销：结果只在内存里、清掉就真没了，所以必须给后悔的
 * 机会；但为它打断一次点击不划算——撤销入口比确认框更轻，也更难点错。
 */
function clearResults(): void {
  const count = results.clearRecords()
  if (count === 0) return
  ui.pushToast({
    kind: 'warn',
    message: t('result.cleared', { count }),
    action: { label: t('common.undo'), run: () => results.restoreCleared() },
    timeout: 8000,
  })
}

/** 复制前几条：按当前排序取，与用户看到的顺序一致。 */
async function copyTop(count: number): Promise<void> {
  const text = results.visible
    .slice(0, count)
    .map((record) => `${record.ip}:${record.port}`)
    .join('\n')
  if (text === '') return
  try {
    await navigator.clipboard.writeText(text)
    ui.pushToast({ kind: 'ok', message: t('common.copied') })
  } catch {
    // 剪贴板在非安全上下文里不可用；此时提示用户手动选，不要静默失败。
    ui.pushToast({ kind: 'warn', message: t('common.copy') })
  }
}

function speedSelected(): void {
  startSpeed('single', results.selectedRecords)
}

/** 「测速本组」走同一条路径：先选中，再按选中发起。 */
function onGroupSpeed(records: typeof results.all): void {
  startSpeed('single', records)
}

/**
 * 工具栏的三种测速范围。
 *
 * 三个入口对应三种目标集。`scope` 不只是给后端看的标签：分地区 TopN 与提前
 * 收敛都只在 `all` 时生效（地区测速是用户点名要测这个地区的全部节点，再截断
 * 或提前收工就等于把用户要的东西砍掉），所以范围必须如实上报。
 */
function onToolbarSpeed(scope: 'single' | 'region' | 'all'): void {
  const targets =
    scope === 'single' ? results.selectedRecords : scope === 'region' ? results.visible : results.all
  startSpeed(scope, targets)
}

/**
 * 发起测速。
 *
 * 参数在这里按当前配置组装好整份发出去。后端拿到的是**完整参数**，不会
 * 替我们去读配置——只发目标和范围的话，设置页里改过的并发、间隔、测速源
 * 就一个都不会生效。
 */
function startSpeed(scope: 'single' | 'region' | 'all', targets: typeof results.all): void {
  if (targets.length === 0) return
  sendCommand(CMD.speedStart, buildSpeedParams(settings.values, { scope, targets }))
  view.value = 'speed'
}

/**
 * 导出当前结果。
 *
 * 字段用当前可见列：用户看到的和导出的必须是同一份东西，否则「导出少了几列」
 * 会被当成丢数据。格式留空表示用后端的默认值。
 */
function exportResult(): void {
  if (results.visible.length === 0) return
  // 带上来历：显示的是历史加载的那一份时，导「最新」会导出完全不同的数据。
  exporter.request({ fields: fields.visibleKeys, id: results.sourceId })
}

/**
 * 按熔断建议改配置。
 *
 * 走 settings/update 而不是只改本地：并发与测速源都是服务端配置，只改本地
 * 的话下一次测速仍然会用回原值——用户会以为建议没生效。
 */
/**
 * 选源说明的文案。
 *
 * 认不出的原因码就退回显示原值：后端加了新原因而前端还没跟上时，至少不会显示
 * 空白——空白会让人以为这个功能坏了。
 */
function sourceReason(code: string): string {
  const key = `speed.source.${code}`
  const translated = t(key as never)
  return translated === key ? code : translated
}

function applyBreakerFix(patch: Record<string, unknown>): void {
  sendCommand(CMD.settingsUpdate, { patch: { speed: patch }, origins: {} })
  speed.dismissBreaker()
}
</script>

<template>
  <div
    class="page"
    @touchstart.passive="narrow && onPullStart($event)"
    @touchmove.passive="narrow && onPullMove($event)"
    @touchend="narrow && onPullEnd()"
  >
    <p v-if="pullDistance > 0" class="pull ct-subtle" :style="{ height: Math.min(pullDistance, 90) + 'px' }">
      {{ pullDistance >= PULL_THRESHOLD ? t('common.retry') : '' }}
    </p>

    <EmptyState
      v-if="showEmpty"
      class="ct-card"
      :title="t('result.empty')"
      :desc="t('result.emptyDesc')"
      :action-label="t('result.emptyAction')"
      @action="ui.activeView = 'scan'"
    />

    <template v-else>
      <Banner
        v-if="speed.breaker && view === 'speed'"
        tone="warn"
        :message="speed.breaker.message"
        :action-label="t('speed.lowerConcurrency', { value: speed.suggestedConcurrency() })"
        @action="applyBreakerFix({ concurrency: speed.suggestedConcurrency() })"
        @close="speed.dismissBreaker()"
      />

      <p v-if="view === 'speed' && speed.source" class="ct-subtle source-line">
        {{ t('speed.source.title') }}：<span class="ct-mono">{{ speed.source.url }}</span>
        · {{ sourceReason(speed.source.code) }}
        <template v-if="speed.source.detail">（{{ speed.source.detail }}）</template>
      </p>

      <div class="stats">
        <div v-for="item in stats" :key="item.label" class="stat">
          <span class="ct-subtle">{{ item.label }}</span>
          <span class="stat-value tnum" :class="item.tone">{{ item.value }}<small>{{ item.unit }}</small></span>
        </div>
      </div>

      <!--
        漏斗条只在「本次任务刚跑过、而且看的就是它的结果」时出现。
        从历史加载的那一份没有过程数据；而刷新后从 /latest 拉回来的结果虽然有
        数据，但漏斗是空的——两种情况显示出来都是一排没有意义的 0。
      -->
      <section v-if="showFunnel" class="ct-card funnel-card">
        <Funnel :steps="funnelSteps" compact />
      </section>

      <RegionChips />

      <ResultToolbar
        v-model:view="view"
        @copy="copyTop"
        @export="exportResult"
        @speed="onToolbarSpeed"
      />

      <RecordCardList v-if="narrow" :records="results.paged" />
      <DataTable v-else v-model:expanded="expanded" :columns="fields.columns" :records="results.paged" @speed="onGroupSpeed" />
      <p class="ct-subtle foot">
        {{ results.visible.length }} / {{ results.total }}
        <button v-if="results.total > 0" type="button" class="ct-link" @click="clearResults">
          {{ t('result.clear') }}
        </button>
      </p>
      <!--
        只有一页时不显示分页控件：一个「第 1 / 1 页」的工具栏只是占地方，
        而绝大多数扫描的结果本来就只有一页。
      -->
      <Pager
        v-if="results.pageCount > 1"
        :page="results.currentPage"
        :page-count="results.pageCount"
        :total="results.visible.length"
        :page-size="ui.pageSize"
        @update:page="results.setPage"
      />

      <MobileActionBar
        v-if="narrow"
        :selected-count="selectedCount"
        :in-speed-view="view === 'speed'"
        @copy="copyTop(results.visible.length)"
        @speed="speedSelected"
        @export="exportResult"
      />
    </template>
  </div>
</template>

<style scoped>
.pull {
  display: flex;
  align-items: center;
  justify-content: center;
  margin: 0;
  overflow: hidden;
  transition: height var(--duration-fast) var(--ease);
}

.page {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.stats {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: var(--space-3);
}

.stat {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border);
  border-top-color: var(--color-border-highlight);
  border-radius: var(--radius-lg);
  background: var(--color-surface);
  box-shadow: var(--elevation-1);
}

.stat-value {
  font-size: var(--font-size-2xl);
  font-weight: 500;
  line-height: 1.2;
}

.stat-value small {
  margin-left: 2px;
  color: var(--color-text-subtle);
  font-size: var(--font-size-sm);
  font-weight: 400;
}

.stat-value.ok {
  color: var(--color-ok);
}

/* 漏斗条：一行说明「这些数字是怎么筛出来的」，比统计卡矮一档。 */
.funnel-card {
  padding: var(--space-3) var(--space-4);
}

.spacer {
  flex: 1;
}

.source-line {
  margin: 0;
  overflow-wrap: anywhere;
}

@media (max-width: 768px) {
  /* 给固定的底部操作栏让位，否则最后一行结果会被压在它下面。 */
  .page {
    padding-bottom: 76px;
  }
}

.foot {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-3);
}
</style>
