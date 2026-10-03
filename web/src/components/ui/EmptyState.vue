<script setup lang="ts">
/**
 * 空状态。
 *
 * 三件事缺一不可：**图形**（说明这里本该有东西）、**一句人话**（说明为什么是空的）、
 * **一个快捷入口**（说明接下来能做什么）。只写「暂无数据」的空表格，用户只能
 * 自己猜下一步。
 *
 * 抽成一个组件是因为此前三个页面各写各的：扫描页有图形、结果页只有文案、历史页
 * 又换了一套措辞。同一件事有三种长相，用户会以为它们不是同一类状态。
 */
import RadarPulse from './RadarPulse.vue'

withDefaults(
  defineProps<{
    title: string
    desc?: string
    /** 快捷入口的文案；不给就不显示按钮。 */
    actionLabel?: string
    /** 是否显示雷达波纹。窄屏下组件自身会隐藏它。 */
    radar?: boolean
  }>(),
  { radar: true },
)

const emit = defineEmits<{ (event: 'action'): void }>()
</script>

<template>
  <div class="empty">
    <RadarPulse v-if="radar" />
    <p class="title">{{ title }}</p>
    <p v-if="desc" class="ct-subtle">{{ desc }}</p>
    <button v-if="actionLabel" type="button" class="ct-btn ct-btn--primary" @click="emit('action')">
      {{ actionLabel }}
    </button>
  </div>
</template>

<style scoped>
.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-4);
  text-align: center;
}

.title {
  font-weight: 500;
}
</style>
