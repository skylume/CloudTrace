<script setup lang="ts">
/**
 * 档位与参数。
 *
 * 关键决定：**选档位与调参数是同一个界面**。档位只是「批量填一组值」，
 * 填完立刻可以改——如果参数藏在另一个「高级模式」里，用户会以为档位是
 * 一道关卡，而规格明确要求它只是快捷填充。
 *
 * 参数的中文标签、范围、说明都来自 i18n 映射表，组件里不硬编码。
 */
import { computed, ref, watch } from 'vue'

import PresetManager from '@/components/scan/PresetManager.vue'
import PresetParams from '@/components/scan/PresetParams.vue'
import Banner from '@/components/ui/Banner.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import { t } from '@/i18n'
import { CUSTOM_PRESET, matchPreset, paramPaths } from '@/i18n/params'
import { useAdaptiveStore, type AdaptiveNotice } from '@/stores/adaptive'
import { usePresetsStore } from '@/stores/presets'
import { useUIStore } from '@/stores/ui'
import { checkScanParams, type ParamWarning } from '@/utils/paramRules'

const params = defineModel<Record<string, number | boolean>>('params', { required: true })
const presetId = defineModel<string>('presetId', { required: true })

defineProps<{ disabled?: boolean }>()

const ui = useUIStore()
const adaptive = useAdaptiveStore()
const presets = usePresetsStore()

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
 * 由 `ui.density` 决定而不是组件自己的状态：披露是「首次默认」，不是每次的
 * 关卡——用户展开过一次，下次进来就该直接是展开的，不该让他再点一次。
 */
const expanded = computed(() => ui.showAdvanced)

/**
 * 展开 / 收起。
 *
 * 用户的最近一次选择说了算：展开记成「高级」，收起记成「简单」。`auto` 只在
 * 用户还没表过态时起作用（默认简单）。这比「展开过就再也收不回去」更符合
 * 直觉，而想回到简单还有 Ctrl+Shift+A。
 */
function toggleExpanded(): void {
  ui.setDensity(expanded.value ? 'simple' : 'advanced')
}

/**
 * 档位下拉的选项。
 *
 * 档位列表来自后端，顺序也由后端定（内置在前、各自按 Order 排）。前端不重排：
 * 两处都排序，用户看到的顺序与后端认定的顺序迟早会不一样。
 */
const segments = computed(() => [
  ...presets.list.map((preset) => ({ value: preset.id, label: preset.name })),
  { value: CUSTOM_PRESET, label: t('preset.custom') },
])

/**
 * 参数一变就重新判定档位。
 *
 * 档位由参数**推**出来，而不是另外记一个状态：改回原值就该自动回到那个档位，
 * 而不是一直挂着「自定义」。换档位时反过来——服务端写完配置广播回来，参数变
 * 了，这里跟着判定成新档位。
 */
watch(
  params,
  () => {
    presetId.value = matchPreset(params.value, presets.list)
  },
  { deep: true },
)

/** 偏离档位时的提示：告诉用户「你已经不在原来的档位上了」。 */
const deviatedFrom = computed(() => {
  if (presetId.value === CUSTOM_PRESET) return ''
  if (matchPreset(params.value, presets.list) !== CUSTOM_PRESET) return ''
  const source = presets.byID(presetId.value)
  return source ? t('preset.deviated', { name: source.name }) : ''
})

/**
 * 换档位。
 *
 * 只发命令，不在本地填值：服务端写完配置会广播回来，面板跟着填。两边各写
 * 一次看着更快，但两次写的来源标记不一样（档位填的标 preset、面板写回的标
 * user），谁后到谁说了算——结果是自适应能不能动这组值变得不确定。
 */
function applyPreset(id: string): void {
  presetId.value = id
  if (id === CUSTOM_PRESET) return
  presets.use(id)
}

// ---- 另存为我的档位 ----

const saving = ref(false)
const draftName = ref('')

function startSave(): void {
  draftName.value = ''
  saving.value = true
}

/** 存的是面板上当前这组值，不是档位里那组——用户要的是「把现在这些存下来」。 */
function confirmSave(): void {
  const name = draftName.value.trim()
  const ok = presets.save({ name, values: paramPaths(params.value) })
  ui.pushToast(
    ok
      ? { kind: 'ok', message: t('preset.saved', { name }) }
      : { kind: 'warn', message: t('preset.saveFailed') },
  )
  if (ok) {
    saving.value = false
    draftName.value = ''
  }
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
  return t(`paramRule.${item.rule}` as never, { a: item.values[0] ?? 0, b: item.values[1] ?? 0 })
}
</script>

<template>
  <section class="ct-card">
    <h2 class="ct-card-title">
      <span>{{ t('preset.summary') }}</span>
      <span v-if="deviatedFrom" class="deviated">{{ deviatedFrom }}</span>
      <span class="spacer" />
      <button type="button" class="ct-link" @click="startSave">{{ t('preset.saveAs') }}</button>
      <button type="button" class="ct-link" @click="toggleExpanded">
        {{ expanded ? t('preset.collapse') : t('preset.all') }}
      </button>
    </h2>

    <!-- 另存为：就地展开一行输入，不弹窗——弹窗会打断「调完参数顺手存一下」这个动作。 -->
    <div v-if="saving" class="save-row">
      <input
        v-model="draftName"
        class="ct-input"
        type="text"
        :placeholder="t('preset.namePlaceholder')"
        @keyup.enter="confirmSave"
        @keyup.esc="saving = false"
      />
      <button type="button" class="ct-btn ct-btn--primary" @click="confirmSave">{{ t('common.save') }}</button>
      <button type="button" class="ct-btn" @click="saving = false">{{ t('common.cancel') }}</button>
    </div>

    <SegmentedControl
      :segments="segments"
      :model-value="presetId"
      :label="t('preset.summary')"
      @update:model-value="applyPreset"
    />

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

    <p v-if="deviatedFrom" class="notice ct-subtle">{{ t('preset.appliesNextRun') }}</p>

    <PresetParams v-model:params="params" :disabled="disabled" />

    <PresetManager />
  </section>
</template>

<style scoped>
.spacer {
  flex: 1;
}

.notice {
  margin: var(--space-2) 0 0;
}

/* 卡片本身是块级容器，子元素之间没有间距，这里补上。 */
.notices {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-top: var(--space-3);
}

.save-row {
  display: flex;
  gap: var(--space-2);
  margin-bottom: var(--space-3);
}

.save-row .ct-input {
  flex: 1;
  min-width: 0;
}

.deviated {
  color: var(--color-warn);
  font-size: var(--font-size-xs);
  font-weight: 400;
}
</style>
