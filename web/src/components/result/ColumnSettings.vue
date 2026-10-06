<script setup lang="ts">
/**
 * 列设置浮层：决定表格显示哪些列、以什么顺序显示。
 *
 * 字段清单来自后端，所以这里列出的是**全部**字段而不是预设里那几列——后端加
 * 一个字段，这里自动就能勾出来，前端不用改。
 *
 * 顺序用「上移 / 下移」而不是拖拽：列数最多十几个，两个按钮就能到任何位置，
 * 而且键盘也能操作。拖拽排序在键盘上等于没有。
 */
import { computed, ref } from 'vue'

import { t } from '@/i18n'
import { useFieldsStore } from '@/stores/fields'

const fields = useFieldsStore()
const open = ref(false)

/** 可见列按当前顺序排；不可见的按后端给的顺序接在后面。 */
const ordered = computed(() => {
  const visible = fields.visibleKeys
    .map((key) => fields.byKey.get(key))
    .filter((field): field is NonNullable<typeof field> => Boolean(field))
  const rest = fields.fields.filter((field) => !fields.visibleKeys.includes(field.key))
  return { visible, rest }
})

/** 一列都不剩时不给通过：空表格比缺一列更难理解。 */
const lastOne = computed(() => fields.visibleKeys.length <= 1)
</script>

<template>
  <div class="anchor">
    <button
      type="button"
      class="ct-btn"
      :aria-expanded="open"
      :title="t('columns.hint')"
      @click="open = !open"
    >
      {{ t('columns.title') }}
    </button>

    <!-- 点空白处收起。用整屏的透明层而不是全局监听：少一处需要清理的副作用。 -->
    <div v-if="open" class="scrim" @click="open = false" />

    <div v-if="open" class="panel">
      <p class="ct-subtle tip">{{ t('columns.hint') }}</p>

      <ul class="list">
        <li v-for="(field, index) in ordered.visible" :key="field.key" class="row">
          <label class="pick">
            <input
              type="checkbox"
              class="ct-check"
              checked
              :disabled="lastOne"
              @change="fields.toggleKey(field.key)"
            />
            <span>{{ field.label }}</span>
          </label>
          <span class="spacer" />
          <button
            type="button"
            class="ct-link"
            :disabled="index === 0"
            :aria-label="t('columns.moveUp')"
            @click="fields.moveKey(field.key, -1)"
          >
            ↑
          </button>
          <button
            type="button"
            class="ct-link"
            :disabled="index === ordered.visible.length - 1"
            :aria-label="t('columns.moveDown')"
            @click="fields.moveKey(field.key, 1)"
          >
            ↓
          </button>
        </li>
      </ul>

      <template v-if="ordered.rest.length > 0">
        <p class="ct-subtle tip">{{ t('columns.hidden') }}</p>
        <ul class="list">
          <li v-for="field in ordered.rest" :key="field.key" class="row">
            <label class="pick">
              <input type="checkbox" class="ct-check" @change="fields.toggleKey(field.key)" />
              <span>{{ field.label }}</span>
            </label>
          </li>
        </ul>
      </template>

      <div class="foot">
        <button type="button" class="ct-link" @click="fields.resetWidths()">{{ t('columns.resetWidths') }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.anchor {
  position: relative;
}

.scrim {
  position: fixed;
  inset: 0;
  z-index: var(--z-dropdown);
}

.panel {
  position: absolute;
  top: calc(100% + var(--space-2));
  right: 0;
  z-index: calc(var(--z-dropdown) + 1);
  width: 260px;
  max-height: 60vh;
  overflow-y: auto;
  padding: var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-surface);
  box-shadow: var(--elevation-2);
}

.tip {
  margin: 0 0 var(--space-2);
  font-size: var(--font-size-xs);
}

.list {
  margin: 0;
  padding: 0;
  list-style: none;
}

.row {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  height: 28px;
  font-size: var(--font-size-sm);
}

.pick {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  cursor: pointer;
}

.spacer {
  flex: 1;
}

.foot {
  margin-top: var(--space-2);
  padding-top: var(--space-2);
  border-top: 1px solid var(--color-border);
  text-align: right;
}
</style>
