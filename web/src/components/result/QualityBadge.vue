<script setup lang="ts">
/**
 * 质量徽章：一行状态的文字出口。
 *
 * 颜色只是辅助。文字标签始终在，色盲、灰度与高对比度模式下结论不丢。
 */
import { t } from '@/i18n'
import type { QualityMark } from '@/utils/quality'

defineProps<{ mark: QualityMark }>()
</script>

<template>
  <span class="ct-quality" :class="`is-${mark.tone}`">{{ t(mark.label) }}</span>
</template>
