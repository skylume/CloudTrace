<script setup lang="ts">
import PageStub from '@/components/ui/PageStub.vue'
</script>

<template>
  <PageStub
    title="历史"
    :points="[
      '列表只读索引，不打开记录文件',
      '加载前提示参数差异：这是 N 分钟前用别的参数跑的',
      '标签 / 备注 / 收藏，收藏不占保留名额',
      '两份记录对比：新增、消失、延迟与排名变化',
    ]"
  />
</template>
