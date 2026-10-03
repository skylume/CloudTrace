<script setup lang="ts">
/**
 * 日志面板：过程可见的最后一道保障。
 *
 * 进度条只说明「进行到哪」，日志说明「发生了什么」——跳过多少节点、哪个
 * 源没拉到、什么时候熔断。用户怀疑「是不是卡死了」时，看的是这里。
 */
import { computed } from 'vue'

import { t } from '@/i18n'
import { formatLogTime, useLogStore } from '@/stores/log'

const log = useLogStore()

const lines = computed(() => log.lines.slice(-60))
</script>

<template>
  <section class="ct-card">
    <h2 class="ct-card-title">
      <span>{{ t('scan.log') }}</span>
      <span class="spacer" />
      <button v-if="lines.length > 0" type="button" class="ct-link" @click="log.clear()">
        {{ t('result.filter.clear') }}
      </button>
    </h2>
    <p v-if="lines.length === 0" class="ct-subtle">{{ t('common.empty') }}</p>
    <ol v-else class="lines ct-mono">
      <li v-for="line in lines" :key="line.at + line.text" :class="line.level">
        <span class="time tnum">{{ formatLogTime(line.at) }}</span>
        <span>{{ line.text }}</span>
      </li>
    </ol>
  </section>
</template>

<style scoped>
.spacer {
  flex: 1;
}

.lines {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 180px;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  list-style: none;
  font-size: var(--font-size-xs);
  line-height: 1.7;
}

.lines li {
  display: flex;
  gap: var(--space-2);
  color: var(--color-text-muted);
}

.time {
  flex: none;
  color: var(--color-text-subtle);
}

.lines li.warn {
  color: var(--color-warn);
}

.lines li.bad {
  color: var(--color-bad);
}
</style>
