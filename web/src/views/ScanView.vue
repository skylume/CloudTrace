<script setup lang="ts">
import PageStub from '@/components/ui/PageStub.vue'
</script>

<template>
  <PageStub
    title="扫描"
    :points="[
      '首屏只放「来源 + 档位 + 开始扫描」，参数默认折叠',
      '来源框实时解析预览，支持拖拽导入与粘贴',
      '右侧常驻筛选漏斗与地区分布，任务进行中实时变化',
      '进度与预计剩余时间在顶栏，切页不丢',
    ]"
  />
</template>
