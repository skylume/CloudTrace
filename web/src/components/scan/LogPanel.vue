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
import { useUIStore } from '@/stores/ui'

const log = useLogStore()
const ui = useUIStore()

const lines = computed(() => log.lines.slice(-60))

/**
 * 日志的纯文本形态。
 *
 * 复制与导出共用它：两处各拼一遍的话，改了时间格式只会改到一处，用户复制出来
 * 的和导出的就对不上。
 */
function asText(): string {
  return log.lines.map((line) => `${formatLogTime(line.at)}  ${line.text}`).join('\n')
}

async function copyAll(): Promise<void> {
  const text = asText()
  if (text === '') return
  try {
    await navigator.clipboard.writeText(text)
    ui.pushToast({ kind: 'ok', message: t('common.copied') })
  } catch {
    // 剪贴板在非安全上下文里不可用；提示用户手动选，不要静默失败。
    ui.pushToast({ kind: 'warn', message: t('common.copy') })
  }
}

/**
 * 导出成文件。
 *
 * 走 Blob + 临时 a 标签而不是后端下载中转：日志本来就在前端手里，为它跑一趟
 * 服务端只是绕路。
 */
function exportLog(): void {
  const text = asText()
  if (text === '') return
  const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')
  const link = document.createElement('a')
  link.href = url
  link.download = `cloudtrace-log-${stamp}.txt`
  link.click()
  URL.revokeObjectURL(url)
}
</script>

<template>
  <section class="ct-card">
    <h2 class="ct-card-title">
      <span>{{ t('scan.log') }}</span>
      <span class="spacer" />
      <template v-if="lines.length > 0">
        <button type="button" class="ct-link" @click="copyAll">{{ t('common.copy') }}</button>
        <button type="button" class="ct-link" @click="exportLog">{{ t('common.export') }}</button>
        <button type="button" class="ct-link" @click="log.clear()">{{ t('result.filter.clear') }}</button>
      </template>
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

/* 标题右侧的三个动作挨在一起，与标题拉开一点距离。 */
.ct-card-title .ct-link {
  margin-left: var(--space-2);
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
