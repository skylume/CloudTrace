<script setup lang="ts">
/**
 * 结果页的移动端底部操作栏。
 *
 * 手机上拿到结果后要做的就三件事：复制、测速、导出。它们在桌面端待在工具栏里，
 * 而工具栏在窄屏会被折成好几行、还可能滚到屏幕外——把这三个动作固定在底部，
 * 手指不用往上找。
 *
 * 图标 + 短文案而不是纯图标：这三个动作的重要性相近，纯图标需要用户先认一遍
 * 图形含义，而底部栏的空间够放两三个字。
 */
import { computed } from 'vue'

import { t } from '@/i18n'

const props = defineProps<{
  /** 已选中的条数，为 0 时测速按钮不可用。 */
  selectedCount: number
  /** 是否处于测速视图——测速视图下这个按钮没有意义。 */
  inSpeedView: boolean
}>()

const emit = defineEmits<{
  (event: 'copy'): void
  (event: 'speed'): void
  (event: 'export'): void
}>()

const speedLabel = computed(() =>
  props.selectedCount > 0 ? t('result.speedSelected', { count: props.selectedCount }) : t('speed.title'),
)
</script>

<template>
  <div class="bar">
    <button type="button" class="action" @click="emit('copy')">
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <rect x="9" y="9" width="11" height="11" rx="2" />
        <path d="M5 15V6a2 2 0 0 1 2-2h8" />
      </svg>
      <span>{{ t('result.copyAll') }}</span>
    </button>

    <button
      type="button"
      class="action"
      :disabled="selectedCount === 0 || inSpeedView"
      @click="emit('speed')"
    >
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M5 12a7 7 0 0 1 14 0M12 12l4-3" />
      </svg>
      <span>{{ speedLabel }}</span>
    </button>

    <button type="button" class="action" @click="emit('export')">
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M12 4v10M8 11l4 4 4-4M5 19h14" />
      </svg>
      <span>{{ t('common.export') }}</span>
    </button>
  </div>
</template>

<style scoped>
.bar {
  display: none;
  position: sticky;
  bottom: 0;
  z-index: var(--z-sticky);
  gap: var(--space-1);
  margin: 0 calc(-1 * var(--space-4));
  padding: var(--space-2) var(--space-4);
  border-top: 1px solid var(--color-border);
  background: var(--color-surface);
}

.action {
  display: flex;
  /* 触摸目标下限 44px；这里是主操作区，给到 48px。 */
  min-height: 48px;
  flex: 1;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  border: 0;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--color-text-muted);
  font-size: var(--font-size-xs);
  cursor: pointer;
}

.action:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.action:not(:disabled):active {
  background: var(--color-surface-hover);
}

.action svg {
  width: 20px;
  height: 20px;
  fill: none;
  stroke: currentcolor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}

/* 文案可能较长（「测速选中 12」），允许折行而不是撑破按钮。 */
.action span {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 768px) {
  .bar {
    display: flex;
  }
}
</style>
