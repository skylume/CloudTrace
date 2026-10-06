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
import TopBar from './TopBar.vue'
import BrandMark from '@/components/ui/BrandMark.vue'
import BottomTabs from './BottomTabs.vue'
import { t } from '@/i18n'
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

/** 窄屏时侧栏变成抽屉、底部出现标签栏。桌面端由用户自己决定是否收起。 */
const narrow = useNarrow()
const drawerOpen = ref(false)

// 从窄屏切回宽屏时把抽屉收掉，否则它会以浮层形式留在宽屏上。
watch(narrow, (isNarrow) => {
  if (!isNarrow) drawerOpen.value = false
})

const collapsed = computed(() => !narrow.value && ui.navCollapsed)

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
      <TopBar
        :title="props.title"
        :narrow="narrow"
        :collapsed="collapsed"
        @toggle-drawer="drawerOpen = !drawerOpen"
        @toggle-collapse="ui.navCollapsed = !ui.navCollapsed"
      />

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

/* ---------- 内容区 ---------- */

.main {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;
}

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
