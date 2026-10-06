<script setup lang="ts">
/**
 * 侧栏导航。
 *
 * 每项带一句说明：只给「扫描 / 结果 / 历史 / 设置」四个词，第一次用的人
 * 得挨个点一遍才知道哪是哪；一句话的成本很低，省掉的是一次次试错。
 *
 * 图标用内联 SVG 而不是图标库：整个界面只用到个位数个图标，为此引一个
 * 依赖不划算，也避免任何外部资源。
 */
import { t } from '@/i18n'

export interface NavItem {
  id: string
  labelKey: string
  descKey: string
}

const props = defineProps<{
  items: NavItem[]
  active: string
  collapsed: boolean
}>()

const emit = defineEmits<{ (event: 'select', id: string): void }>()

/** 图标路径。统一 24×24 视框、线性描边，与界面其余部分的笔画粗细一致。 */
const ICONS: Record<string, string> = {
  scan: 'M4 6h16M4 12h10M4 18h7M17 14l3 3 3-3',
  result: 'M4 5h16v14H4zM4 10h16M9 10v9',
  history: 'M12 7v5l3 2M4 12a8 8 0 1 0 2.3-5.6M4 4v4h4',
  settings: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM4 12h2M18 12h2M12 4v2M12 18v2M6.3 6.3l1.4 1.4M16.3 16.3l1.4 1.4M17.7 6.3l-1.4 1.4M7.7 16.3l-1.4 1.4',
}
</script>

<template>
  <nav class="nav" :aria-label="t('nav.main')">
    <button
      v-for="item in props.items"
      :key="item.id"
      type="button"
      class="item"
      :class="{ active: props.active === item.id, collapsed: props.collapsed }"
      :aria-current="props.active === item.id ? 'page' : undefined"
      :title="props.collapsed ? t(item.labelKey) : undefined"
      @click="emit('select', item.id)"
    >
      <svg class="icon" viewBox="0 0 24 24" aria-hidden="true">
        <path :d="ICONS[item.id] ?? ICONS.result" />
      </svg>
      <span v-if="!props.collapsed" class="text">
        <span class="label">{{ t(item.labelKey) }}</span>
        <span class="desc">{{ t(item.descKey) }}</span>
      </span>
    </button>
  </nav>
</template>

<style scoped>
.nav {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-3) var(--space-2);
  overflow-y: auto;
}

.item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-3);
  border: 0;
  position: relative;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--color-text-muted);
  text-align: left;
  cursor: pointer;
  transition:
    background-color var(--duration-fast) var(--ease),
    color var(--duration-fast) var(--ease);
}

.item.collapsed {
  justify-content: center;
  padding: var(--space-3) 0;
}

.item:hover {
  background: var(--color-surface-hover);
  color: var(--color-text);
}

.item.active {
  background: var(--color-primary-soft);
  color: var(--color-primary-text);
}

/* 左侧竖条：比整块实心轻，但足够把「当前在哪」说清楚。 */
.item.active::before {
  content: '';
  position: absolute;
  left: 0;
  top: 20%;
  bottom: 20%;
  width: 2px;
  border-radius: var(--radius-pill);
  background: var(--color-primary);
}

.icon {
  width: 18px;
  height: 18px;
  flex: 0 0 18px;
  fill: none;
  stroke: currentcolor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.text {
  display: flex;
  min-width: 0;
  flex-direction: column;
}

.label {
  font-size: var(--font-size-md);
  font-weight: 500;
}

.desc {
  margin-top: 1px;
  font-size: var(--font-size-xs);
  color: var(--color-text-subtle);
}

.item.active .desc {
  color: inherit;
  opacity: 0.8;
}
</style>
