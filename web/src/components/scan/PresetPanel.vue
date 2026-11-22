<script setup lang="ts">
/**
 * 参数面板。
 *
 * 只做三件事：渲染参数、提示组合不合理的取值、把自适应建议摆出来。选深度在
 * 动作行里（DepthPicker），改默认值在设置页——这个面板不承担任何「保存」语义。
 *
 * 参数的中文标签、范围、说明都来自 i18n 映射表，组件里不硬编码。
 */
import { computed } from 'vue'

import PresetParams from '@/components/scan/PresetParams.vue'
import Banner from '@/components/ui/Banner.vue'
import { t } from '@/i18n'
import { useAdaptiveStore, type AdaptiveNotice } from '@/stores/adaptive'
import { useUIStore } from '@/stores/ui'
import { checkScanParams, type ParamWarning } from '@/utils/paramRules'

const params = defineModel<Record<string, number | boolean>>('params', { required: true })

defineProps<{ disabled?: boolean }>()

const ui = useUIStore()
const adaptive = useAdaptiveStore()

/**
 * 建议的整句话。
 *
 * 在脚本里拼好而不是写进模板：模板里的模板字面量容易被工具链吞掉（这个仓库
 * 已经被坑过三次），而且拼装逻辑放在这里也更好读。
 */
function suggestionText(item: AdaptiveNotice): string {
  return adaptive.reasonText(item.reason) + '　' + item.key + ' ' + item.from + ' → ' + item.to
}

/**
 * 是否展开全部参数。
 *
 * 由 ui.density 决定而不是组件自己的状态：披露是「首次默认」，不是每次的
 * 关卡——用户展开过一次，下次进来就该直接是展开的，不该让他再点一次。
 */
const expanded = computed(() => ui.showAdvanced)

/**
 * 展开 / 收起。
 *
 * 用户的最近一次选择说了算：展开记成「高级」，收起记成「简单」。auto 只在
 * 用户还没表过态时起作用（默认简单）。这比「展开过就再也收不回去」更符合
 * 直觉，而想回到简单还有 Ctrl+Shift+A。
 */
function toggleExpanded(): void {
  ui.setDensity(expanded.value ? 'simple' : 'advanced')
}

/**
 * 组合起来不合理的参数。
 *
 * 与「超出建议区间」是两件事：那是个体越界，这里是两项都合法、搭在一起却
 * 会互相抵消。两者都只提示——参数怎么设是用户的事，界面负责让他知道代价。
 */
const warnings = computed<ParamWarning[]>(() => checkScanParams(params.value))

/** 把命中时的取值填进文案。模板里不拼字符串，避免被工具链吃掉。 */
function warningText(item: ParamWarning): string {
  return t(('paramRule.' + item.rule) as never, { a: item.values[0] ?? 0, b: item.values[1] ?? 0 })
}
</script>

<template>
  <section class="ct-card">
    <h2 class="ct-card-title">
      <span>{{ t('param.panel') }}</span>
      <span class="spacer" />
      <button type="button" class="ct-link" @click="toggleExpanded">
        {{ expanded ? t('param.collapse') : t('param.all') }}
      </button>
    </h2>

    <div v-if="adaptive.visibleSuggestions.length > 0 || warnings.length > 0" class="notices">
      <!-- 建议：值没有变，等用户自己决定。绝不替他点。 -->
      <Banner
        v-for="item in adaptive.visibleSuggestions"
        :key="item.key"
        tone="info"
        :message="suggestionText(item)"
        :action-label="t('adaptive.accept')"
        @action="adaptive.accept(item)"
        @close="adaptive.dismiss(item.key)"
      />

      <!--
        组合问题：只提示，不改值，也不给「一键修复」——该改哪一边取决于
        用户想干什么，界面猜不出来。因此这里连关闭都不给，改到不冲突为止。
      -->
      <Banner
        v-for="item in warnings"
        :key="item.rule"
        tone="warn"
        :message="warningText(item)"
        :note="t('paramRule.onlyHint')"
        :closable="false"
      />
    </div>

    <p class="persist-hint">{{ t('param.persistHint') }}</p>

    <PresetParams v-model:params="params" :disabled="disabled" />
  </section>
</template>

<style scoped>
.spacer {
  flex: 1;
}

/* 卡片本身是块级容器，子元素之间没有间距，这里补上。 */
.notices {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-top: var(--space-3);
}

.persist-hint {
  margin: var(--space-3) 0 0;
  color: var(--color-text-subtle);
  font-size: var(--font-size-xs);
}
</style>
