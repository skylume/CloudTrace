<script setup lang="ts">
/**
 * 右键菜单。
 *
 * 位置由调用方给（鼠标坐标），这里只做两件事：贴到不超出视口的位置、点空白
 * 或按 Esc 收起。不做「跟随父容器滚动」那类花活——菜单是浮在最上层的，
 * 打开期间用户不会去滚表格。
 */
import { onBeforeUnmount, onMounted, ref } from 'vue'

import type { ContextMenuItem } from './contextMenu'

const props = defineProps<{ x: number; y: number; items: ContextMenuItem[] }>()
const emit = defineEmits<{ (event: 'close'): void }>()

const menu = ref<HTMLElement | null>(null)
const pos = ref({ x: props.x, y: props.y })

/** 贴边：先按鼠标位置摆，摆出去就拉回来。 */
const MARGIN = 8

onMounted(() => {
  window.addEventListener('keydown', onKey)
  const rect = menu.value?.getBoundingClientRect()
  if (!rect) return
  pos.value = {
    x: Math.max(MARGIN, Math.min(props.x, window.innerWidth - rect.width - MARGIN)),
    y: Math.max(MARGIN, Math.min(props.y, window.innerHeight - rect.height - MARGIN)),
  }
})

onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

function onKey(event: KeyboardEvent): void {
  if (event.key === 'Escape') emit('close')
}

function pick(item: ContextMenuItem): void {
  if (item.disabled) return
  item.action()
  emit('close')
}
</script>

<template>
  <!-- 整屏透明层：点任何地方都收起。右键也收起，避免叠出第二个菜单。 -->
  <div class="scrim" @click="emit('close')" @contextmenu.prevent="emit('close')" />
  <ul ref="menu" class="menu" role="menu" :style="{ left: `${pos.x}px`, top: `${pos.y}px` }">
    <li v-for="item in props.items" :key="item.label">
      <button type="button" role="menuitem" :disabled="item.disabled" @click="pick(item)">
        {{ item.label }}
      </button>
    </li>
  </ul>
</template>

<style scoped>
.scrim {
  position: fixed;
  inset: 0;
  z-index: var(--z-dialog);
}

.menu {
  position: fixed;
  z-index: calc(var(--z-dialog) + 1);
  min-width: 160px;
  margin: 0;
  padding: var(--space-1);
  list-style: none;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-surface);
  box-shadow: var(--elevation-2);
}

.menu button {
  display: block;
  width: 100%;
  padding: 0 var(--space-3);
  height: 28px;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text);
  font-size: var(--font-size-sm);
  text-align: left;
  cursor: pointer;
}

.menu button:hover:not(:disabled) {
  background: var(--color-surface-hover);
}

.menu button:disabled {
  color: var(--color-text-subtle);
  cursor: default;
}
</style>
