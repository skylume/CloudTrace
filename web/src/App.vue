<script setup lang="ts">
/**
 * 应用外壳 + 页面切换。
 *
 * 四个页面用一个字符串状态切换，不引路由库：页面之间没有 URL 语义需求，
 * 而路由会带来 history 与打包拆分两处额外复杂度，对这个体量的应用不划算。
 * 当前页面记在本地，下次打开直接回到上次停留的地方。
 */
import { computed } from 'vue'

import AppShell from '@/components/layout/AppShell.vue'
import type { NavItem } from '@/components/layout/SideNav.vue'
import { t } from '@/i18n'
import { useUIStore } from '@/stores/ui'

import HistoryView from '@/views/HistoryView.vue'
import ResultView from '@/views/ResultView.vue'
import ScanView from '@/views/ScanView.vue'
import SettingsView from '@/views/SettingsView.vue'

const ui = useUIStore()

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
</template>
