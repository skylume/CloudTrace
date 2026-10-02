<script setup lang="ts">
/**
 * 页面占位。
 *
 * 只用于「外壳已经能跑、页面还没写」这段过渡期：把每页要放什么列清楚，
 * 免得后面忘了当初为什么这么排。页面写完即删除。
 */
defineProps<{ title: string; points: string[] }>()
</script>

<template>
  <div class="stub">
    <h2>{{ title }}</h2>
    <ul>
      <li v-for="point in points" :key="point">{{ point }}</li>
    </ul>
  </div>
</template>

<style scoped>
.stub {
  max-width: 640px;
  padding: var(--space-5);
  border: 1px dashed var(--color-border-strong);
  border-radius: var(--radius-lg);
  background: var(--color-surface);
}

h2 {
  margin-bottom: var(--space-3);
}

ul {
  margin: 0;
  padding-left: 1.1em;
  color: var(--color-text-muted);
  font-size: var(--font-size-sm);
  line-height: 2;
}
</style>
