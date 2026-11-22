<script setup lang="ts">
/**
 * 扫描深度选择。
 *
 * 三档是「一次填充」，不是持续状态：点一下把值写进面板，之后用户改什么都不影响
 * 这个高亮。因此这里没有「已偏离」、没有另存为，也没有下拉里越来越长的「我的档位」。
 */
import { t } from '@/i18n'
import { SCAN_DEPTHS, type ScanDepth } from '@/i18n/depth'

defineProps<{ depth: ScanDepth; disabled?: boolean }>()

const emit = defineEmits<{ (event: 'apply', id: ScanDepth): void }>()
</script>

<template>
  <div class="depth" role="group" :aria-label="t('depth.label')">
    <button
      v-for="item in SCAN_DEPTHS"
      :key="item.id"
      type="button"
      class="ct-btn depth-btn"
      :class="{ 'ct-btn--primary': item.id === depth }"
      :disabled="disabled"
      :title="t(item.noteKey as never)"
      @click="emit('apply', item.id)"
    >
      {{ t(item.labelKey as never) }}
    </button>
    <span class="hint">{{ t('depth.hint') }}</span>
  </div>
</template>

<style scoped>
.depth {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}

.depth-btn {
  min-width: 64px;
}

.hint {
  color: var(--color-text-subtle);
  font-size: var(--font-size-xs);
}
</style>
