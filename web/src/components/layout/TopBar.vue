<script setup lang="ts">
/**
 * 顶栏：回答「现在怎么样」。
 *
 * 任务进度刻意放在这里而不是内容区：切到别的页面时进度不该消失。同理，后台
 * 下载库文件的细进度条也贴在这里——它属于「现在在发生什么」，不属于任何一页。
 */
import { computed } from 'vue'

import { durationText, t } from '@/i18n'
import { useGeoStore } from '@/stores/geo'
import { useTaskStore } from '@/stores/task'
import { useUIStore } from '@/stores/ui'

const props = defineProps<{
  title: string
  /** 窄屏：侧栏是抽屉，左侧按钮改成开抽屉。 */
  narrow: boolean
  collapsed: boolean
}>()

const emit = defineEmits<{
  (event: 'toggleDrawer'): void
  (event: 'toggleCollapse'): void
}>()

const task = useTaskStore()
const ui = useUIStore()
const geo = useGeoStore()

/** 后台正在下载库文件。 */
const downloading = computed(() => geo.status?.status.downloading === true)

/**
 * 下载进度百分比；服务端没给总长度时返回 null。
 *
 * 拿不到总长度就不能编一个百分比出来——那种进度条走到一半突然跳完，比一条
 * 「进行中」的动画更让人不信任。
 */
const downloadPercent = computed<number | null>(() => {
  const status = geo.status?.status
  if (!status?.downloading || status.download_total <= 0) return null
  return Math.min(100, Math.round((status.download_read / status.download_total) * 100))
})

const downloadLabel = computed(() =>
  downloadPercent.value === null
    ? t('geo.downloading')
    : t('geo.downloadingPercent', { percent: downloadPercent.value }),
)

function cycleTheme(): void {
  const order = ['system', 'dark', 'light'] as const
  const index = order.indexOf(ui.theme)
  // 走 store 的 setter 而不是直接赋值：它会同时写回服务端，否则下次拉配置
  // 就把这次切换覆盖掉了。
  ui.setTheme(order[(index + 1) % order.length] ?? 'system')
}

function toggleLang(): void {
  ui.setLang(ui.lang === 'zh' ? 'en' : 'zh')
}
</script>

<template>
  <header class="topbar">
    <button
      v-if="props.narrow"
      type="button"
      class="icon-button"
      :aria-label="t('nav.main')"
      @click="emit('toggleDrawer')"
    >
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M4 12h16M4 17h16" /></svg>
    </button>
    <button
      v-else
      type="button"
      class="icon-button"
      :aria-label="t(props.collapsed ? 'nav.expandSidebar' : 'nav.collapseSidebar')"
      @click="emit('toggleCollapse')"
    >
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 6h16M4 12h10M4 18h16" /></svg>
    </button>

    <h1 class="title">{{ props.title }}</h1>

    <!--
      进度用 aria-live 播报：读屏用户看不到进度条，只能靠播报知道任务在动。
      用 polite 而不是 assertive——它每 250ms 变一次，assertive 会把其他内容全压掉。
    -->
    <div v-if="task.running" class="task-strip" role="status" aria-live="polite" aria-atomic="false">
      <span class="task-phase">{{ t(`task.phase.${task.phase}`) }}</span>
      <span
        class="track"
        role="progressbar"
        :aria-valuenow="task.percent"
        aria-valuemin="0"
        aria-valuemax="100"
        :aria-label="t(`task.phase.${task.phase}`)"
      >
        <span class="fill" :style="{ width: `${task.percent}%` }" />
      </span>
      <span class="task-num tnum">
        {{ task.state.done }} / {{ task.state.total }}
        <template v-if="task.state.eta_s > 0">
          · {{ t('task.remaining', { value: durationText(task.state.eta_s) }) }}
        </template>
      </span>
    </div>

    <span class="spacer" />

    <span v-if="downloading" class="hint" :title="downloadLabel">{{ downloadLabel }}</span>
    <span v-else-if="geo.status?.status.loaded" class="hint">{{ t('geo.title') }} {{ t('geo.ready') }}</span>
    <button type="button" class="icon-button" :aria-label="t('theme.label')" @click="cycleTheme">
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6l1.4 1.4M17 17l1.4 1.4M18.4 5.6L17 7M7 17l-1.4 1.4" />
        <circle cx="12" cy="12" r="3.5" />
      </svg>
    </button>
    <button type="button" class="text-button" @click="toggleLang">{{ t('lang.switch') }}</button>

    <!--
      后台下载库文件：一条贴着顶栏下沿的细进度条。不占位置、不弹窗，也不阻断
      任何操作——它只是让用户知道「它在做事」，而不是以为程序卡住了。
    -->
    <div
      v-if="downloading"
      class="download-bar"
      :class="{ indeterminate: downloadPercent === null }"
      role="progressbar"
      :aria-label="downloadLabel"
      :aria-valuenow="downloadPercent ?? undefined"
    >
      <span class="fill" :style="downloadPercent === null ? undefined : { width: downloadPercent + '%' }" />
    </div>
  </header>
</template>

<style scoped>
.topbar {
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
  display: flex;
  align-items: center;
  gap: var(--space-3);
  height: var(--topbar-height);
  padding: 0 var(--space-4);
  border-bottom: 1px solid var(--color-border);
  background: var(--color-surface);
}

/* 细进度条贴在顶栏下沿：不占一行、不抢任务进度条的位置。 */
.download-bar {
  position: absolute;
  inset: auto 0 -1px;
  height: 2px;
  overflow: hidden;
  background: var(--color-surface-hover);
}

.download-bar .fill {
  display: block;
  height: 100%;
  background: var(--color-primary);
  transition: width var(--duration-fast) linear;
}

/* 服务端没给总长度时改走「来回滑动」，表示「在动，但不知道还有多久」。 */
.download-bar.indeterminate .fill {
  width: 30%;
  animation: download-slide 1.4s ease-in-out infinite;
}

@keyframes download-slide {
  0% {
    transform: translateX(-100%);
  }
  100% {
    transform: translateX(400%);
  }
}

.title {
  font-size: var(--font-size-lg);
  font-weight: 500;
  white-space: nowrap;
}

.spacer {
  flex: 1;
}

.task-strip {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  font-size: var(--font-size-xs);
  color: var(--color-text-muted);
}

.task-phase,
.task-num {
  white-space: nowrap;
}

.track {
  width: 96px;
  height: 5px;
  border-radius: var(--radius-pill);
  background: var(--color-border);
  overflow: hidden;
}

.fill {
  display: block;
  height: 100%;
  border-radius: var(--radius-pill);
  background: var(--color-primary);
  transition: width var(--duration-normal) var(--ease);
}

.hint {
  color: var(--color-text-subtle);
  font-size: var(--font-size-xs);
  white-space: nowrap;
}

.icon-button {
  display: inline-flex;
  width: var(--control-height);
  height: var(--control-height);
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
}

.icon-button:hover {
  background: var(--color-surface-hover);
  color: var(--color-text);
}

.icon-button svg {
  width: 18px;
  height: 18px;
  fill: none;
  stroke: currentcolor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.text-button {
  height: var(--control-height);
  padding: 0 var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--color-text-muted);
  font-size: var(--font-size-xs);
  cursor: pointer;
}

.text-button:hover {
  background: var(--color-surface-hover);
  color: var(--color-text);
}
</style>
