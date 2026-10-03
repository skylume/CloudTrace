<script setup lang="ts">
/**
 * 全局提示栈。
 *
 * 它是「可逆操作」的落点：删除历史、清空结果这类动作不弹确认框，而是先做掉
 * 再给一个撤销入口——撤销比确认轻，而且用户是在看到结果之后才决定要不要撤，
 * 这比事前猜「我是不是点错了」准得多。
 *
 * 危险色只用于真的失败：成功与提醒用中性或警告色，否则满屏红色会让人对错误
 * 脱敏。
 */
import { t } from '@/i18n'
import { useUIStore } from '@/stores/ui'

const ui = useUIStore()
</script>

<template>
  <div class="stack" role="status" aria-live="polite">
    <div v-for="toast in ui.toasts" :key="toast.id" class="toast" :class="toast.kind">
      <span class="text">{{ toast.message }}</span>
      <button v-if="toast.action" type="button" class="action" @click="toast.action.run()">
        {{ toast.action.label }}
      </button>
      <button type="button" class="close" :aria-label="t('common.close')" @click="ui.dismissToast(toast.id)">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18" /></svg>
      </button>
    </div>
  </div>
</template>

<style scoped>
.stack {
  position: fixed;
  right: var(--space-4);
  bottom: var(--space-4);
  z-index: var(--z-toast);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  max-width: min(420px, calc(100vw - var(--space-6)));
  pointer-events: none;
}

.toast {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-surface-raised);
  box-shadow: var(--elevation-overlay);
  font-size: var(--font-size-sm);
  pointer-events: auto;
}

.toast.ok {
  border-color: var(--color-ok-border);
}

.toast.warn {
  border-color: var(--color-warn-border);
}

.toast.bad {
  border-color: var(--color-bad-border);
  color: var(--color-bad);
}

.text {
  min-width: 0;
}

.action {
  padding: 2px var(--space-3);
  border: 1px solid var(--color-primary);
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-primary-text);
  font-size: var(--font-size-xs);
  white-space: nowrap;
  cursor: pointer;
}

.action:hover {
  background: var(--color-primary-soft);
}

.close {
  display: inline-flex;
  width: 20px;
  height: 20px;
  align-items: center;
  justify-content: center;
  border: 0;
  background: transparent;
  color: var(--color-text-subtle);
  cursor: pointer;
}

.close svg {
  width: 14px;
  height: 14px;
  fill: none;
  stroke: currentcolor;
  stroke-width: 1.8;
  stroke-linecap: round;
}

/* 窄屏铺满底部：手机上右下角的小条很难点中。 */
@media (max-width: 768px) {
  .stack {
    right: var(--space-3);
    left: var(--space-3);
    bottom: var(--space-3);
    max-width: none;
  }
}
</style>
