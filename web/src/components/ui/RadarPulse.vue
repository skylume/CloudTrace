<script setup lang="ts">
/**
 * 雷达波纹：表达「机器正在找东西」。
 *
 * 只用在两个地方——扫描进行中、以及「还没有结果」的空状态。它的作用是让
 * 等待有个形状，而不是装饰：同心圆扩散本身就是「在搜索」的通用隐喻。
 *
 * 可关闭（`data-animation="off"` 与 `prefers-reduced-motion` 都由基础样式
 * 兜住），窄屏默认不显示——手机上没有多余的空间和电量留给动效。
 */
withDefaults(defineProps<{ size?: number }>(), { size: 78 })
</script>

<template>
  <div class="radar" :style="{ width: `${size}px`, height: `${size}px` }" aria-hidden="true">
    <span />
    <span />
    <span />
    <i class="core" />
  </div>
</template>

<style scoped>
.radar {
  position: relative;
}

.radar span {
  position: absolute;
  inset: 0;
  border: 1px solid var(--color-primary);
  border-radius: 50%;
  opacity: 0;
  animation: radar-pulse 2.4s var(--ease) infinite;
}

.radar span:nth-child(2) {
  animation-delay: 0.8s;
}

.radar span:nth-child(3) {
  animation-delay: 1.6s;
}

.core {
  position: absolute;
  left: 50%;
  top: 50%;
  width: 9px;
  height: 9px;
  margin: -4.5px 0 0 -4.5px;
  border-radius: 50%;
  background: var(--color-primary);
}

/* 从中心扩散到边缘并淡出：起点小、终点大。 */
@keyframes radar-pulse {
  0% {
    transform: scale(0.18);
    opacity: 0.55;
  }

  70% {
    opacity: 0.12;
  }

  100% {
    transform: scale(1);
    opacity: 0;
  }
}

@media (max-width: 768px) {
  .radar {
    display: none;
  }
}
</style>
