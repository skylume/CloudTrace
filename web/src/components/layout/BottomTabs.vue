<script setup lang="ts">
/**
 * 底部标签栏：窄屏下的主导航。
 *
 * 手机上的导航必须落在拇指够得着的区域。侧栏抽屉也能用，但每次切页都要
 * 「点开抽屉 → 选页 → 抽屉收起」，三步；底部标签一步到位。
 *
 * 抽屉保留下来装连接状态这类「看一眼就好」的信息，不再承担主导航。
 */
import type { NavItem } from './SideNav.vue'
import { t } from '@/i18n'

const props = defineProps<{
  items: NavItem[]
  active: string
}>()

const emit = defineEmits<{ (event: 'select', id: string): void }>()

/** 图标与侧栏共用同一套路径，避免同一件事有两套画法。 */
const ICONS: Record<string, string> = {
  scan: 'M4 6h16M4 12h10M4 18h7M17 14l3 3 3-3',
  result: 'M4 5h16v14H4zM4 10h16M9 10v9',
  history: 'M12 7v5l3 2M4 12a8 8 0 1 0 2.3-5.6M4 4v4h4',
  settings: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM4 12h2M18 12h2M12 4v2M12 18v2',
}
</script>

<template>
  <nav class="tabs" aria-label="主导航">
    <button
      v-for="item in props.items"
      :key="item.id"
      type="button"
      class="tab"
      :class="{ on: props.active === item.id }"
      :aria-current="props.active === item.id ? 'page' : undefined"
      @click="emit('select', item.id)"
    >
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path :d="ICONS[item.id] ?? ICONS.result" />
      </svg>
      <span>{{ t(item.labelKey) }}</span>
    </button>
  </nav>
</template>

<style scoped>
.tabs {
  position: sticky;
  bottom: 0;
  z-index: var(--z-sticky);
  display: none;
  border-top: 1px solid var(--color-border);
  background: var(--color-surface);
  /* 有安全区的机型（刘海屏横条）要留出空间，否则最后一个按钮压在系统手势区上。 */
  padding-bottom: env(safe-area-inset-bottom, 0);
}

.tab {
  display: flex;
  /* 触摸目标下限 44px：低于这个尺寸拇指容易点偏。 */
  min-height: 48px;
  flex: 1;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  border: 0;
  background: transparent;
  color: var(--color-text-muted);
  font-size: var(--font-size-xs);
  cursor: pointer;
}

.tab.on {
  color: var(--color-primary-text);
}

.tab svg {
  width: 20px;
  height: 20px;
  fill: none;
  stroke: currentcolor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}

@media (max-width: 768px) {
  .tabs {
    display: flex;
  }
}
</style>
