<script setup lang="ts">
/**
 * 横幅：用于「不阻断但必须告知」的信息。
 *
 * 代理出口提示、429 熔断、需要重新扫描才生效——这类信息都不需要用户立刻
 * 决策，所以**绝不用弹窗**：弹窗会打断流程，而它们只是提醒。
 *
 * 四态一律「淡底 + 描边 + 深字」，不用实心色块——实心横幅在页面上太重，
 * 和结果表里成片的状态色也会打架。
 */
import { t } from '@/i18n'

const props = withDefaults(
  defineProps<{
    tone?: 'info' | 'ok' | 'warn' | 'bad'
    /** 一句话说明发生了什么。 */
    message: string
    /** 可选的「怎么办」，直接写动作，不要写「点击这里」。 */
    actionLabel?: string
    /** 是否可关闭。熔断这类必须看到的提示不给关闭。 */
    closable?: boolean
  }>(),
  { tone: 'info', closable: true },
)

const emit = defineEmits<{ (event: 'action'): void; (event: 'close'): void }>()

/** 图标路径：四态各有自己的形状，不靠颜色单独区分。 */
const ICONS: Record<string, string> = {
  info: 'M12 8h.01M11 12h1v4h1',
  ok: 'M5 13l4 4L19 7',
  warn: 'M12 8v4M12 16h.01M10.3 4.3 2.6 17.4A2 2 0 0 0 4.3 20.4h15.4a2 2 0 0 0 1.7-3L13.7 4.3a2 2 0 0 0-3.4 0z',
  bad: 'M12 8v4M12 16h.01M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z',
}
</script>

<template>
  <div class="banner" :class="props.tone" role="status">
    <svg class="icon" viewBox="0 0 24 24" aria-hidden="true">
      <path :d="ICONS[props.tone]" />
    </svg>
    <span class="text">{{ props.message }}</span>
    <button v-if="props.actionLabel" type="button" class="action" @click="emit('action')">
      {{ props.actionLabel }}
    </button>
    <span class="spacer" />
    <button v-if="props.closable" type="button" class="close" :aria-label="t('common.close')" @click="emit('close')">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18" /></svg>
    </button>
  </div>
</template>

<style scoped>
.banner {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-3);
  border: 1px solid;
  border-radius: var(--radius-md);
  font-size: var(--font-size-sm);
}

.banner.info {
  border-color: var(--color-info-border);
  background: var(--color-info-bg);
  color: var(--color-info);
}

.banner.ok {
  border-color: var(--color-ok-border);
  background: var(--color-ok-bg);
  color: var(--color-ok);
}

.banner.warn {
  border-color: var(--color-warn-border);
  background: var(--color-warn-bg);
  color: var(--color-warn);
}

.banner.bad {
  border-color: var(--color-bad-border);
  background: var(--color-bad-bg);
  color: var(--color-bad);
}

.icon,
.close svg {
  width: 16px;
  height: 16px;
  flex: 0 0 16px;
  fill: none;
  stroke: currentcolor;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.text {
  min-width: 0;
}

.spacer {
  flex: 1;
}

.action {
  padding: 3px var(--space-3);
  border: 1px solid currentcolor;
  border-radius: var(--radius-sm);
  background: transparent;
  color: inherit;
  font-size: var(--font-size-xs);
  white-space: nowrap;
  cursor: pointer;
}

.action:hover {
  background: rgb(255 255 255 / 12%);
}

.close {
  display: inline-flex;
  width: 24px;
  height: 24px;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: inherit;
  opacity: 0.7;
  cursor: pointer;
}

.close:hover {
  opacity: 1;
}
</style>
