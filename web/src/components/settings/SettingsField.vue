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
import { fieldText, type SettingField } from '@/i18n/settingsSchema'

const props = defineProps<{
  field: SettingField
  value: unknown
  locale: 'zh' | 'en'
}>()

const emit = defineEmits<{
  (event: 'change', path: string, value: unknown): void
  (event: 'reset', path: string): void
}>()

const text = computed(() => fieldText(props.field.path, props.locale))
const hint = computed(() => (text.value.hint === '' ? t('common.none') : text.value.hint))

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
  const clamped = Math.min(Math.max(value, props.field.min ?? 0), props.field.max ?? Number.MAX_SAFE_INTEGER)
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
</script>

<template>
  <div class="field">
    <label class="label" :title="hint">
      <span>{{ text.label }}</span>
      <span class="info" aria-hidden="true">?</span>
    </label>

    <div class="control">
      <input
        v-if="field.kind === 'bool'"
        type="checkbox"
        class="ct-check"
        :checked="Boolean(value)"
        @change="emit('change', field.path, ($event.target as HTMLInputElement).checked)"
      />

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
        v-else-if="field.kind === 'int'"
        type="number"
        class="ct-input tnum"
        :value="Number(value ?? 0)"
        :min="field.min"
        :max="field.max"
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

      <span v-if="field.min !== undefined" class="ct-subtle range tnum">
        {{ field.min }}–{{ field.max }}
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
  align-items: center;
  gap: var(--space-2);
  font-size: var(--font-size-sm);
  cursor: help;
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
