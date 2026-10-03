<script setup lang="ts">
/**
 * 应用外壳 + 页面切换。
 *
 * 四个页面用一个字符串状态切换，不引路由库：页面之间没有 URL 语义需求，
 * 而路由会带来 history 与打包拆分两处额外复杂度，对这个体量的应用不划算。
 * 当前页面记在本地，下次打开直接回到上次停留的地方。
 */
import { computed, ref } from 'vue'

import AppShell from '@/components/layout/AppShell.vue'
import CommandPalette from '@/components/layout/CommandPalette.vue'
import ToastStack from '@/components/ui/ToastStack.vue'
import type { NavItem } from '@/components/layout/SideNav.vue'
import { sendCommand } from '@/api/client'
import { t } from '@/i18n'
import { useActionStore } from '@/stores/actions'
import { useResultsStore } from '@/stores/results'
import { useTaskStore } from '@/stores/task'
import { useUIStore } from '@/stores/ui'
import { useHotkeys } from '@/utils/hotkeys'

import HistoryView from '@/views/HistoryView.vue'
import ResultView from '@/views/ResultView.vue'
import ScanView from '@/views/ScanView.vue'
import SettingsView from '@/views/SettingsView.vue'

const ui = useUIStore()
const task = useTaskStore()
const results = useResultsStore()
const actions = useActionStore()

const paletteOpen = ref(false)

/** 复制选中的节点，格式为 ip:port。 */
async function copySelected(): Promise<void> {
  const text = results.selectedRecords.map((record) => `${record.ip}:${record.port}`).join('\n')
  if (text === '') return
  try {
    await navigator.clipboard.writeText(text)
    ui.pushToast({ kind: 'ok', message: t('common.copied') })
  } catch {
    ui.pushToast({ kind: 'warn', message: t('common.copy') })
  }
}

/**
 * 全局快捷键。
 *
 * Esc 的优先级是「先关浮层、再停任务」：面板开着的时候按 Esc，用户想关的是
 * 面板，而不是把正在跑的任务停掉。
 */
useHotkeys([
  { key: 'k', ctrl: true, allowInInput: true, handler: () => (paletteOpen.value = !paletteOpen.value) },
  {
    key: 'enter',
    ctrl: true,
    allowInInput: true,
    handler: () => {
      if (paletteOpen.value) return
      actions.runStartScan()
    },
  },
  {
    key: 'escape',
    allowInInput: true,
    handler: () => {
      if (paletteOpen.value) {
        paletteOpen.value = false
        return
      }
      if (task.running) sendCommand('scan/stop')
    },
  },
  {
    key: 'f',
    ctrl: true,
    handler: () => {
      if (ui.activeView !== 'result') ui.activeView = 'result'
      // 搜索框在结果页工具栏里，用 id 定位比层层透传 ref 简单得多。
      requestAnimationFrame(() => document.getElementById('ct-result-search')?.focus())
    },
  },
  { key: 'a', ctrl: true, shift: true, handler: () => ui.setDensity(ui.showAdvanced ? 'simple' : 'advanced') },
  { key: 'c', ctrl: true, handler: () => void copySelected() },
])

const NAV: NavItem[] = [
  { id: 'scan', labelKey: 'nav.scan', descKey: 'nav.scan.desc' },
  { id: 'result', labelKey: 'nav.result', descKey: 'nav.result.desc' },
  { id: 'history', labelKey: 'nav.history', descKey: 'nav.history.desc' },
  { id: 'settings', labelKey: 'nav.settings', descKey: 'nav.settings.desc' },
]

const VIEWS = { scan: ScanView, result: ResultView, history: HistoryView, settings: SettingsView } as const

type ViewId = keyof typeof VIEWS

const activeId = computed<ViewId>(() => (ui.activeView in VIEWS ? (ui.activeView as ViewId) : 'scan'))
const activeComponent = computed(() => VIEWS[activeId.value])

/** 顶栏标题与侧栏共用同一份文案，改一处就够。 */
const title = computed(() => t(`nav.${activeId.value}` as never))
</script>

<template>
  <AppShell :items="NAV" :active="activeId" :title="title" @select="ui.activeView = $event">
    <component :is="activeComponent" />
  </AppShell>
  <!-- 浮层挂在外壳之外：它们是全局的，不该被内容区的滚动或布局影响。 -->
  <CommandPalette v-model="paletteOpen" />
  <ToastStack />
</template>
