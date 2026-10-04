<script setup lang="ts">
/**
 * 应用外壳：侧栏 + 顶栏 + 内容区。
 *
 * 三件事的归属是固定的：
 *   - 侧栏管「去哪儿」，顶栏管「现在怎么样」，内容区管「这件事本身」；
 *   - 任务进度放顶栏而不是内容区：切到别的页面时进度不该消失；
 *   - 连接状态放侧栏底部而不是顶栏：它是个长期状态，不该和临时提示抢位置。
 */
import { computed, ref, watch } from 'vue'

import SideNav, { type NavItem } from './SideNav.vue'
import BrandMark from '@/components/ui/BrandMark.vue'
import BottomTabs from './BottomTabs.vue'
import { t } from '@/i18n'
import { useGeoStore } from '@/stores/geo'
import { useTaskStore } from '@/stores/task'
import { useUIStore } from '@/stores/ui'
import { useNarrow } from '@/utils/useMediaQuery'

const props = defineProps<{
  items: NavItem[]
  active: string
  title: string
}>()

const emit = defineEmits<{ (event: 'select', id: string): void }>()

const task = useTaskStore()
const ui = useUIStore()
const geo = useGeoStore()

/** 窄屏时侧栏变成抽屉、底部出现标签栏。桌面端由用户自己决定是否收起。 */
const narrow = useNarrow()
const drawerOpen = ref(false)

// 从窄屏切回宽屏时把抽屉收掉，否则它会以浮层形式留在宽屏上。
watch(narrow, (isNarrow) => {
  if (!isNarrow) drawerOpen.value = false
})

const collapsed = computed(() => !narrow.value && ui.navCollapsed)

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

const connectionLabel = computed(() => {
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

function formatSeconds(seconds: number): string {
  if (!seconds || seconds <= 0) return '—'
  if (seconds < 60) return `${seconds.toFixed(0)} 秒`
  const minutes = Math.floor(seconds / 60)
  return `${minutes} 分 ${Math.round(seconds - minutes * 60)} 秒`
}

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

function select(id: string): void {
  drawerOpen.value = false
  emit('select', id)
}
</script>

<template>
  <div class="shell">
    <aside class="sidebar" :class="{ collapsed, drawer: narrow, open: drawerOpen }">
      <div class="brand">
        <BrandMark :size="collapsed ? 22 : 26" />
        <span v-if="!collapsed" class="name">{{ t('app.name') }}</span>
      </div>

      <SideNav :items="props.items" :active="props.active" :collapsed="collapsed" @select="select" />

      <div class="foot">
        <span class="dot" :class="task.connection" aria-hidden="true" />
        <span v-if="!collapsed" class="foot-text">{{ connectionLabel }}</span>
      </div>
    </aside>

    <div v-if="narrow && drawerOpen" class="scrim" @click="drawerOpen = false" />

    <section class="main">
      <header class="topbar">
        <button
          v-if="narrow"
          type="button"
          class="icon-button"
          :aria-label="t('nav.scan')"
          @click="drawerOpen = !drawerOpen"
        >
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M4 12h16M4 17h16" /></svg>
        </button>
        <button
          v-else
          type="button"
          class="icon-button"
          :aria-label="collapsed ? '展开侧栏' : '收起侧栏'"
          @click="ui.navCollapsed = !ui.navCollapsed"
        >
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 6h16M4 12h10M4 18h16" /></svg>
        </button>

        <h1 class="title">{{ props.title }}</h1>

        <div v-if="task.running" class="task-strip">
          <span class="task-phase">{{ t(`task.phase.${task.phase}`) }}</span>
          <span class="track"><span class="fill" :style="{ width: `${task.percent}%` }" /></span>
          <span class="task-num tnum">
            {{ task.state.done }} / {{ task.state.total }}
            <template v-if="task.state.eta_s > 0"> · 剩 {{ formatSeconds(task.state.eta_s) }}</template>
          </span>
        </div>

        <span class="spacer" />

        <span v-if="downloading" class="hint" :title="downloadLabel">{{ downloadLabel }}</span>
        <span v-else-if="geo.status?.status.loaded" class="hint">{{ t('geo.title') }} 就绪</span>
        <button type="button" class="icon-button" :aria-label="t('theme.label')" @click="cycleTheme">
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6l1.4 1.4M17 17l1.4 1.4M18.4 5.6L17 7M7 17l-1.4 1.4" />
            <circle cx="12" cy="12" r="3.5" />
          </svg>
        </button>
        <button type="button" class="text-button" @click="toggleLang">
          {{ ui.lang === 'zh' ? 'EN' : '中' }}
        </button>
        <!--
          后台下载库文件：一条贴着顶栏下沿的细进度条。不占位置、不弹窗，也不
          阻断任何操作——它只是让用户知道「它在做事」，而不是以为程序卡住了。
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

      <div class="content">
        <slot />
      </div>

      <!-- 窄屏下的主导航。抽屉保留下来装连接状态这类「看一眼就好」的信息。 -->
      <BottomTabs v-if="narrow" :items="props.items" :active="props.active" @select="select" />
    </section>
  </div>
</template>

<style scoped>
.shell {
  display: flex;
  min-height: 100%;
}

/* ---------- 侧栏 ---------- */

.sidebar {
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
  display: flex;
  width: var(--sidebar-width);
  flex: 0 0 var(--sidebar-width);
  height: 100vh;
  flex-direction: column;
  background: var(--color-surface);
  border-right: 1px solid var(--color-border);
  transition: width var(--duration-normal) var(--ease);
}

.sidebar.collapsed {
  width: 64px;
  flex-basis: 64px;
}

.brand {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  height: var(--topbar-height);
  padding: 0 var(--space-4);
  border-bottom: 1px solid var(--color-border);
}

.sidebar.collapsed .brand {
  justify-content: center;
  padding: 0;
}

.name {
  color: var(--color-text);
  font-size: var(--font-size-md);
  font-weight: 500;
}

.foot {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  border-top: 1px solid var(--color-border);
  color: var(--color-text-subtle);
  font-size: var(--font-size-xs);
}

.sidebar.collapsed .foot {
  justify-content: center;
  padding: var(--space-3) 0;
}

.dot {
  width: 8px;
  height: 8px;
  flex: 0 0 8px;
  border-radius: 50%;
  background: var(--color-text-subtle);
}

.dot.open {
  background: var(--color-ok);
}

.dot.closed {
  background: var(--color-bad);
}

.foot-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* ---------- 顶栏 ---------- */

.main {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;
}

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

.task-phase {
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

.task-num {
  white-space: nowrap;
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

/* ---------- 内容区 ---------- */

.content {
  flex: 1;
  min-height: 0;
  padding: var(--space-5) var(--space-5) var(--space-7);
}

/* ---------- 窄屏：侧栏变抽屉 ---------- */

.sidebar.drawer {
  position: fixed;
  top: 0;
  left: 0;
  height: 100vh;
  width: var(--sidebar-width);
  flex-basis: var(--sidebar-width);
  transform: translateX(-100%);
  transition: transform var(--duration-normal) var(--ease);
  box-shadow: var(--elevation-overlay);
}

.sidebar.drawer.open {
  transform: translateX(0);
}

.scrim {
  position: fixed;
  inset: 0;
  z-index: var(--z-dropdown);
  background: rgb(15 23 42 / 48%);
}

@media (max-width: 900px) {
  .content {
    /* 底部多留一截：Tab 栏是 sticky 的，不留白最后一行会被压在它下面。 */
    padding: var(--space-4) var(--space-4) var(--space-7);
  }
}
</style>
