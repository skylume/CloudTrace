<script setup lang="ts">
/**
 * 应用外壳。
 *
 * 当前只搭到「能证明链路通了」这一层：连接状态、任务快照、漏斗、主题与语言
 * 切换。页面的信息架构与导航等布局方案确认之后再往里填——先把地基跑通，
 * 免得布局一改就把已完成的部分推翻重来。
 */
import { computed } from 'vue'

import { t } from '@/i18n'
import { useGeoStore } from '@/stores/geo'
import { useResultsStore } from '@/stores/results'
import { useSettingsStore } from '@/stores/settings'
import { useTaskStore } from '@/stores/task'
import { useUIStore } from '@/stores/ui'

const task = useTaskStore()
const ui = useUIStore()
const settings = useSettingsStore()
const results = useResultsStore()
const geo = useGeoStore()

const connectionText = computed(() => {
  switch (task.connection) {
    case 'open':
      return t('conn.connected')
    case 'connecting':
      return t('conn.connecting')
    default:
      return task.retryInSeconds > 0
        ? t('conn.reconnecting', { seconds: task.retryInSeconds })
        : t('conn.disconnected')
  }
})

const funnelRows = computed(() => [
  { label: t('funnel.generated'), value: task.funnel.generated },
  { label: t('funnel.latencyOk'), value: task.funnel.latency_ok },
  { label: t('funnel.regionOk'), value: task.funnel.region_ok },
  { label: t('funnel.usable'), value: task.funnel.usable },
])

function formatSeconds(seconds: number): string {
  if (!seconds || seconds <= 0) return '—'
  if (seconds < 60) return `${seconds.toFixed(1)} s`
  const minutes = Math.floor(seconds / 60)
  return `${minutes} 分 ${Math.round(seconds - minutes * 60)} 秒`
}

function cycleTheme(): void {
  const order = ['system', 'dark', 'light'] as const
  const index = order.indexOf(ui.theme)
  ui.theme = order[(index + 1) % order.length] ?? 'system'
}
</script>

<template>
  <div class="shell">
    <header class="strip">
      <span class="dot" :class="task.connection" />
      <span class="muted">{{ connectionText }}</span>
      <span class="sep">·</span>
      <span class="muted">{{ t(`task.${task.state.status}`) }}</span>
      <span v-if="task.running" class="sep">·</span>
      <span v-if="task.running" class="tnum muted">
        {{ task.state.done }} / {{ task.state.total }}
        <template v-if="task.state.eta_s > 0"> · {{ t('task.eta') }} {{ formatSeconds(task.state.eta_s) }}</template>
      </span>
      <span class="spacer" />
      <button type="button" class="ghost" @click="cycleTheme">{{ t(`theme.${ui.theme}`) }}</button>
      <button type="button" class="ghost" @click="ui.lang = ui.lang === 'zh' ? 'en' : 'zh'">
        {{ ui.lang === 'zh' ? 'EN' : '中' }}
      </button>
    </header>

    <main class="body">
      <h1>{{ t('app.name') }}</h1>
      <p class="muted">{{ t('app.tagline') }}</p>

      <div class="grid">
        <div class="cell">
          <div class="muted small">{{ t('task.progress') }}</div>
          <div class="big tnum">{{ task.percent }}%</div>
        </div>
        <div class="cell">
          <div class="muted small">{{ t('task.elapsed') }}</div>
          <div class="big tnum">{{ formatSeconds(task.state.elapsed_s) }}</div>
        </div>
        <div class="cell">
          <div class="muted small">{{ t('task.preset') }}</div>
          <div class="big">{{ task.state.preset || '—' }}</div>
        </div>
        <div class="cell">
          <div class="muted small">{{ t('result.stat.total') }}</div>
          <div class="big tnum">{{ results.total }}</div>
        </div>
      </div>

      <h2>{{ t('funnel.generated') }}</h2>
      <table class="funnel">
        <tbody>
          <tr v-for="row in funnelRows" :key="row.label">
            <td>{{ row.label }}</td>
            <td class="tnum right">{{ row.value }}</td>
          </tr>
        </tbody>
      </table>

      <h2>{{ t('geo.title') }}</h2>
      <p class="muted small">
        {{ t('geo.source') }}：{{ geo.status?.status.source ?? '—' }} ·
        {{ t('geo.records') }}：{{ geo.status?.status.records ?? 0 }} ·
        {{ geo.loaded ? t('conn.connected') : t('common.loading') }}
      </p>
      <p v-if="geo.warning" class="warn">{{ geo.warning.message }}</p>
      <p v-if="settings.restartRequired.length" class="warn">
        {{ t('settings.restartRequired', { keys: settings.restartRequired.join(', ') }) }}
      </p>

      <p class="muted small">
        这是前端骨架：链路（内嵌静态资源 → WebSocket → 事件总线 → store）已打通，
        页面信息架构待定。
      </p>
    </main>
  </div>
</template>

<style scoped>
.shell {
  display: flex;
  flex-direction: column;
  min-height: 100%;
}

.strip {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  border-bottom: 1px solid var(--color-border);
  background: var(--color-surface);
  font-size: var(--font-size-sm);
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
}

.spacer {
  flex: 1;
}

.sep {
  color: var(--color-text-subtle);
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--color-text-subtle);
}

.dot.open {
  background: var(--color-ok);
}

.dot.closed {
  background: var(--color-bad);
}

.muted {
  color: var(--color-text-muted);
}

.small {
  font-size: var(--font-size-sm);
}

.warn {
  color: var(--color-warn);
  font-size: var(--font-size-sm);
}

.body {
  flex: 1;
  width: 100%;
  max-width: var(--content-max);
  margin: 0 auto;
  padding: var(--space-5) var(--space-4) var(--space-7);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

h2 {
  margin-top: var(--space-4);
  color: var(--color-text-muted);
  font-size: var(--font-size-sm);
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: var(--space-3);
  margin-top: var(--space-3);
}

.cell {
  padding: var(--space-3) var(--space-4);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-surface);
}

.big {
  font-size: var(--font-size-xl);
}

.funnel {
  width: 100%;
  border-collapse: collapse;
}

.funnel td {
  padding: var(--space-2) var(--space-3);
  border-bottom: 1px solid var(--color-border);
}

.right {
  text-align: right;
}

.ghost {
  padding: var(--space-1) var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  background: transparent;
  cursor: pointer;
  font-size: var(--font-size-xs);
  color: var(--color-text-muted);
}

.ghost:hover {
  background: var(--color-surface-hover);
  color: var(--color-text);
}
</style>
