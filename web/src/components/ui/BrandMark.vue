<script setup lang="ts">
/**
 * 品牌标记：圆角方块底 + 三条递减横线。
 *
 * 三条横线就是信号条，也就是「云迹」这个名字的图形化——正在追踪的信号。
 * 最长那条用品牌橙，其余用底色。品牌识别靠形状而不是特效：不做渐变、
 * 不做发光、不做立体，16px 下仍要能认出来。
 */
withDefaults(defineProps<{ size?: number }>(), { size: 26 })
</script>

<template>
  <span class="mark" :style="{ width: `${size}px`, height: `${size}px` }" aria-hidden="true">
    <i />
    <i />
    <i />
  </span>
</template>

<style scoped>
.mark {
  display: grid;
  align-content: center;
  gap: 2.5px;
  flex: none;
  padding: 0 5px;
  border-radius: var(--radius-sm);
  /* 底用文字色：浅色主题下是深底浅线，深色主题下自动反过来。 */
  background: var(--color-text);
}

.mark i {
  display: block;
  height: 2.5px;
  border-radius: 1.5px;
  background: var(--color-bg);
}

.mark i:nth-child(1) {
  width: 100%;
  background: var(--color-brand);
}

.mark i:nth-child(2) {
  width: 68%;
}

.mark i:nth-child(3) {
  width: 38%;
}
</style>
