<script setup lang="ts">
/**
 * 网络诊断面板。
 *
 * 与「配置体检」并排放在设置页，但查的是两件不同的事：体检看配置对不对，
 * 这里看网络通不通。用户说「扫不出结果」时，先跑这个能把范围缩到一半。
 *
 * 四项按「一层套一层」的顺序排（DNS → TCP → trace → 出口），顺着往下看就
 * 知道最早断在哪一层——这比四个并列的绿灯有用得多。
 *
 * 逐项的 `summary` 与 `advice` 由后端给：结论依赖实测数值（「解析很慢
 * （820ms）」这种话拼不出来模板），而判断逻辑与扫描时那条代理横幅共用一套，
 * 分两处实现迟早会分叉。
 */
import { computed } from 'vue'

import { t } from '@/i18n'
import type { DiagStatus } from '@/api/types'
import { useDiagStore } from '@/stores/diag'

const diag = useDiagStore()

/** 结论对应的字形。用符号而不是只给颜色：色盲与高对比度下也读得出。 */
const GLYPH: Record<DiagStatus, string> = { ok: '✓', warn: '!', bad: '✕' }

const overall = computed(() => {
  const report = diag.report
  if (!report) return ''
  return t(`diag.overall.${report.status}` as never, { ms: report.elapsed_ms })
})
</script>

<template>
  <section class="ct-card">
    <h2 class="ct-card-title">
      <span>{{ t('diag.title') }}</span>
      <span class="spacer" />
      <span v-if="overall" class="ct-subtle overall">{{ overall }}</span>
    </h2>
    <p class="ct-subtle hint">{{ t('diag.hint') }}</p>

    <div class="actions">
      <button type="button" class="ct-btn" :disabled="diag.running" @click="diag.run()">
        {{ diag.running ? t('diag.running') : t('diag.run') }}
      </button>
      <button type="button" class="ct-btn" :disabled="diag.exporting" @click="diag.exportBundle()">
        {{ t('diag.export') }}
      </button>
    </div>

    <ul v-if="diag.report" class="items">
      <li v-for="item in diag.report.items" :key="item.key" class="item" :class="item.status">
        <div class="head">
          <span class="glyph" aria-hidden="true">{{ GLYPH[item.status] }}</span>
          <span class="label">{{ t(`diag.item.${item.key}` as never) }}</span>
          <span class="summary">{{ item.summary }}</span>
        </div>
        <p v-if="item.detail" class="ct-mono detail">{{ item.detail }}</p>
        <p v-if="item.advice" class="advice">{{ item.advice }}</p>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.spacer {
  flex: 1;
}

.overall {
  font-size: var(--font-size-xs);
  font-weight: 400;
}

.hint {
  margin: 0 0 var(--space-3);
  font-size: var(--font-size-sm);
}

.actions {
  display: flex;
  gap: var(--space-2);
}

.items {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: var(--space-4) 0 0;
  padding: 0;
  list-style: none;
}

.item {
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border);
  border-left-width: 3px;
  border-radius: var(--radius-md);
  background: var(--color-surface);
}

/* 左侧竖条表达结论，与结果表里的状态表达方式一致。 */
.item.ok {
  border-left-color: var(--color-ok);
}

.item.warn {
  border-left-color: var(--color-warn);
}

.item.bad {
  border-left-color: var(--color-bad);
}

.head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-2);
  font-size: var(--font-size-sm);
}

.glyph {
  font-weight: 600;
}

.item.ok .glyph {
  color: var(--color-ok);
}

.item.warn .glyph {
  color: var(--color-warn);
}

.item.bad .glyph {
  color: var(--color-bad);
}

.label {
  min-width: 84px;
  color: var(--color-text-muted);
}

.summary {
  color: var(--color-text);
}

.detail {
  margin: var(--space-1) 0 0 22px;
  font-size: var(--font-size-xs);
  color: var(--color-text-subtle);
  overflow-wrap: anywhere;
}

.advice {
  margin: var(--space-1) 0 0 22px;
  font-size: var(--font-size-xs);
  color: var(--color-text-muted);
}
</style>
