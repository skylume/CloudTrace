<script setup lang="ts">
/**
 * 一个设置项。
 *
 * 每项都带一句「为什么要调」——只给一个数字输入框和它的名字，用户只能靠猜。
 * 说明放在 tooltip 里而不是常显：设置页有几十项，全部常显会让真正需要读的
 * 那一条被淹没。
 *
 * 单独一项也能恢复默认：用户改坏了一个值，不该被逼着把整组都重置掉。
 */
import { computed } from 'vue'


import { t } from '@/i18n'
import { fieldText, highlightParts, readPath, type SettingField } from '@/i18n/settingsSchema'

const props = defineProps<{
  field: SettingField
  value: unknown
  /** 全量配置：field.maxPath 要按它去解析联动的那一项。 */
  values: Record<string, unknown>
  /**
   * 这一项的告警：值合法，但可能带来麻烦（并发过高、阈值过低）。
   *
   * 只标色不拦截——用户有权这么设，但该知道代价。拦下来是另一回事：那会让
   * 「网卡够好、路由够强」的人没法用。
   */
  warning?: { reason: string }
  locale: 'zh' | 'en'
  /** 搜索词：命中的字标出来，说明里命中时把说明提到行下。 */
  highlight?: string
}>()

/**
 * 数值上限。
 *
 * 优先取 maxPath 指向的那一项（如并发跟着「并发上限」走）。取不到就退回声明里
 * 的静态值——宁可范围松一点，也不要让输入框因为联动项还没到而没有上限。
 */
const maxValue = computed(() => {
  if (props.field.maxPath === undefined) return props.field.max
  const linked = readPath(props.values, props.field.maxPath)
  return typeof linked === 'number' ? linked : props.field.max
})

const emit = defineEmits<{
  (event: 'change', path: string, value: unknown): void
  (event: 'reset', path: string): void
}>()

const text = computed(() => fieldText(props.field.path, props.locale))
const hint = computed(() => (text.value.hint === '' ? t('common.none') : text.value.hint))
const labelParts = computed(() => highlightParts(text.value.label, props.highlight ?? ''))

/**
 * 名称没命中、说明命中的情况。
 *
 * 这时把说明提到行下显示：只高亮名称的话，用户搜「限速」看到一整屏没有任何
 * 标记的行，会以为搜索坏了。
 */
const hintHit = computed(() => {
  const needle = (props.highlight ?? '').trim().toLowerCase()
  if (needle === '') return false
  return !text.value.label.toLowerCase().includes(needle) && text.value.hint.toLowerCase().includes(needle)
})

/**
 * 枚举取值的文案。
 *
 * 用统一的 opt.* 键而不是按字段路径拼键：同一个取值（如 off、auto）在多个
 * 字段里含义相同，按路径拼就得为每个字段各写一遍，改一处必漏一处。
 */
function optionLabel(option: string): string {
  const key = `opt.${option}`
  const translated = t(key as never)
  // 没收录的取值回退显示原值，至少还能看出后端给的是什么。
  return translated === key ? option : translated
}

function onNumber(raw: string): void {
  const value = Number(raw)
  if (!Number.isFinite(value)) return
  const clamped = Math.min(Math.max(value, props.field.min ?? 0), maxValue.value ?? Number.MAX_SAFE_INTEGER)
  emit('change', props.field.path, clamped)
}

function onList(raw: string): void {
  const items = raw
    .split(/[,，\s]+/)
    .map((item) => item.trim())
    .filter((item) => item !== '')
  emit('change', props.field.path, items)
}

const listValue = computed(() => (Array.isArray(props.value) ? props.value.join(', ') : ''))

/** 多选当前选中的选项。 */
const chosen = computed(() => (Array.isArray(props.value) ? props.value.map(String) : []))

function onToggle(option: string, on: boolean): void {
  const next = on ? [...chosen.value, option] : chosen.value.filter((item) => item !== option)
  emit('change', props.field.path, next)
}
</script>

<template>
  <div class="field" :class="{ warned: props.warning !== undefined }">
    <label class="label" :title="props.warning ? props.warning.reason : hint">
      <span class="label-text">
        <span v-for="(part, index) in labelParts" :key="index" :class="{ hit: part.hit }">{{ part.text }}</span>
      </span>
      <span class="info" aria-hidden="true">?</span>
      <!-- 保存即写盘、但要重启才换过去，说在行上而不是只靠顶部横幅。 -->
      <span v-if="field.restart" class="restart-tag">{{ t('settings.restartTag') }}</span>
      <span v-if="hintHit" class="match-hint">{{ text.hint }}</span>
    </label>

    <div class="control">
      <input
        v-if="field.kind === 'bool'"
        type="checkbox"
        class="ct-check"
        :checked="Boolean(value)"
        @change="emit('change', field.path, ($event.target as HTMLInputElement).checked)"
      />

      <div v-else-if="field.kind === 'multi'" class="multi">
        <label v-for="option in Object.keys(field.toggles ?? {})" :key="option" class="multi-item">
          <input
            type="checkbox"
            class="ct-check"
            :checked="chosen.includes(option)"
            @change="onToggle(option, ($event.target as HTMLInputElement).checked)"
          />
          <span>{{ optionLabel(option) }}</span>
        </label>
      </div>

      <select
        v-else-if="field.kind === 'enum'"
        class="ct-input"
        :value="String(value ?? '')"
        @change="emit('change', field.path, ($event.target as HTMLSelectElement).value)"
      >
        <option v-for="option in field.options ?? []" :key="option" :value="option">
          {{ optionLabel(option) }}
        </option>
      </select>

      <input
        v-else-if="field.kind === 'int' || field.kind === 'float'"
        type="number"
        class="ct-input tnum"
        :value="Number(value ?? 0)"
        :min="field.min"
        :max="maxValue"
        :step="field.kind === 'float' ? (field.step ?? 0.1) : 1"
        @input="onNumber(($event.target as HTMLInputElement).value)"
      />

      <input
        v-else-if="field.kind === 'list'"
        type="text"
        class="ct-input"
        :value="listValue"
        @change="onList(($event.target as HTMLInputElement).value)"
      />

      <input
        v-else
        type="text"
        class="ct-input"
        :class="{ 'ct-mono': field.kind === 'path' }"
        :value="String(value ?? '')"
        @change="emit('change', field.path, ($event.target as HTMLInputElement).value)"
      />

      <span v-if="props.warning" class="warn-note" :title="props.warning.reason">
        {{ t('settings.warned') }}
      </span>
      <span v-else-if="field.min !== undefined && field.kind !== 'float'" class="ct-subtle range tnum">
        {{ field.min }}–{{ maxValue }}
      </span>
      <button type="button" class="ct-link reset" @click="emit('reset', field.path)">
        {{ t('settings.resetItem') }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.field {
  display: grid;
  grid-template-columns: minmax(0, 200px) minmax(0, 1fr);
  gap: var(--space-3);
  align-items: center;
  padding: var(--space-2) 0;
  border-bottom: 1px solid var(--color-border);
}

.label {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--font-size-sm);
  cursor: help;
}

/* 只把命中的字标出来：整行标色等于没标。 */
.label-text .hit {
  padding: 0 1px;
  border-radius: 2px;
  background: var(--color-warn-bg);
  box-shadow: inset 0 -1px 0 var(--color-warn-border);
}

.match-hint {
  flex-basis: 100%;
  color: var(--color-text-muted);
  font-size: var(--font-size-xs);
  line-height: 1.4;
}

.info {
  display: inline-flex;
  width: 14px;
  height: 14px;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--color-border-strong);
  border-radius: 50%;
  color: var(--color-text-subtle);
  font-size: 10px;
}

.control {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  min-width: 0;
}

.multi {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-4);
}

.multi-item {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--font-size-sm);
  cursor: pointer;
}

.restart-tag {
  padding: 0 var(--space-2);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-pill);
  color: var(--color-text-subtle);
  font-size: 10px;
  white-space: nowrap;
}

/* 有代价的值：输入框描边转警示色，并在原「范围」的位置写一句为什么。 */
.field.warned .ct-input,
.field.warned .ct-check {
  border-color: var(--color-warn);
}

.warn-note {
  color: var(--color-warn);
  font-size: var(--font-size-xs);
  white-space: nowrap;
  cursor: help;
}

/* 下拉的选项文字比输入框长得多（如「GeoLite2（查询更快）」），
   给一个下限宽度，别让它被后面的「恢复默认」挤到显示不全。 */
.control select.ct-input {
  flex: 0 1 auto;
  min-width: 180px;
  max-width: 320px;
}

.control input.ct-input {
  flex: 1;
  max-width: 280px;
}

.range {
  white-space: nowrap;
}

.reset {
  white-space: nowrap;
}

@media (max-width: 768px) {
  .field {
    grid-template-columns: minmax(0, 1fr);
    gap: var(--space-2);
  }
}
</style>
