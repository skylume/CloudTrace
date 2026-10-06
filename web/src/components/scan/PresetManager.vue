<script setup lang="ts">
/**
 * 我的档位：管理自定义档位。
 *
 * 折叠起来放在参数卡片里，不占首屏：档位管理是「配一次就不再进」的事，而首屏
 * 要给的是「点一下就开始扫」。内置三档不出现在这里——它们不可改不可删，列出来
 * 只会让这一屏看起来像有六个可操作的档位。
 *
 * 删除与导入都有明确的反馈：两者都会改变用户手上有哪些档位，静默失败会让用户
 * 以为操作生效了。
 */
import { computed, ref } from 'vue'

import { t } from '@/i18n'
import { paramPaths } from '@/i18n/params'
import { usePresetsStore } from '@/stores/presets'
import { useUIStore } from '@/stores/ui'
import { exportPresets, parsePresets } from '@/utils/presetIO'

/**
 * 面板上当前这组参数。
 *
 * 「覆盖保存」与「存为副本」用的都是它，而不是档位里存着的那组：用户改完参数
 * 再点这两个按钮，要的显然是「把现在这些存下来」。
 */
const params = defineModel<Record<string, number | boolean>>('params', { required: true })

const presets = usePresetsStore()
const ui = useUIStore()

const open = ref(false)
/** 正在编辑的档位：改名或存副本，两者共用一个表单。 */
const form = ref<{ mode: 'rename' | 'copy'; id: string; name: string; note: string } | null>(null)
/** 等待确认删除的档位标识。删除不可恢复，先问一次。 */
const deletingID = ref('')

const rows = computed(() => presets.custom)
const deletingName = computed(() => presets.byID(deletingID.value)?.name ?? '')

function startRename(preset: { id: string; name: string; note?: string }): void {
  form.value = { mode: 'rename', id: preset.id, name: preset.name, note: preset.note ?? '' }
  deletingID.value = ''
}

/** 存副本：名字先填一个「原名 + 副本」，用户想改再改。 */
function startCopy(preset: { id: string; name: string; note?: string }): void {
  form.value = { mode: 'copy', id: preset.id, name: `${preset.name} ${t('preset.copySuffix')}`, note: preset.note ?? '' }
  deletingID.value = ''
}

function confirmForm(): void {
  const draft = form.value
  if (!draft) return
  const name = draft.name.trim()
  if (name === '') return

  const ok =
    draft.mode === 'rename'
      ? presets.rename(draft.id, name)
      : presets.save({ name, note: draft.note.trim(), values: paramPaths(params.value) })

  ui.pushToast(
    ok ? { kind: 'ok', message: t('preset.saved', { name }) } : { kind: 'warn', message: t('preset.saveFailed') },
  )
  if (ok) form.value = null
}

/** 覆盖保存：把面板上现在这组值写回这个档位，名字与备注不动。 */
function overwrite(preset: { id: string; name: string; note?: string }): void {
  const ok = presets.save({
    id: preset.id,
    name: preset.name,
    note: preset.note ?? '',
    values: paramPaths(params.value),
  })
  ui.pushToast(
    ok
      ? { kind: 'ok', message: t('preset.overwritten', { name: preset.name }) }
      : { kind: 'warn', message: t('preset.saveFailed') },
  )
}

function confirmDelete(): void {
  presets.remove(deletingID.value)
  deletingID.value = ''
}

/** 导出成一份文件。用 Blob 而不是走后端：档位本来就在前端手上，绕一圈没意义。 */
function doExport(): void {
  const blob = new Blob([exportPresets(rows.value)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = 'cloudtrace-presets.json'
  link.click()
  URL.revokeObjectURL(url)
}

async function doImport(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  // 清空 value，否则连续选同一个文件不会再触发 change。
  input.value = ''
  if (!file) return

  try {
    const count = presets.importMany(parsePresets(await file.text()))
    ui.pushToast({ kind: 'ok', message: t('preset.imported', { count }) })
  } catch (err) {
    ui.pushToast({
      kind: 'bad',
      message: t('preset.importFailed', { reason: err instanceof Error ? err.message : String(err) }),
    })
  }
}
</script>

<template>
  <div class="manager">
    <button type="button" class="ct-link disclosure" :aria-expanded="open" @click="open = !open">
      {{ open ? '▾' : '▸' }} {{ t('preset.mine') }} ({{ rows.length }})
    </button>

    <template v-if="open">
      <p v-if="rows.length === 0" class="ct-subtle">{{ t('preset.empty') }}</p>

      <ul v-else class="rows">
        <li v-for="(item, index) in rows" :key="item.id">
          <span class="name">
            {{ item.name }}
            <span v-if="item.id === presets.defaultID" class="badge">{{ t('preset.defaultBadge') }}</span>
          </span>
          <!-- 置顶就是「往上移到底」：常用档位排前面，一次点到位比连点几次省事。 -->
          <button
            type="button"
            class="ct-link"
            :disabled="index === 0"
            @click="presets.move(item.id, -index)"
          >
            {{ t('preset.moveTop') }}
          </button>
          <button
            type="button"
            class="ct-link"
            :disabled="index === 0"
            @click="presets.move(item.id, -1)"
          >
            {{ t('preset.moveUp') }}
          </button>
          <button
            type="button"
            class="ct-link"
            :disabled="index === rows.length - 1"
            @click="presets.move(item.id, 1)"
          >
            {{ t('preset.moveDown') }}
          </button>
          <!-- 覆盖保存：把面板上现在这组值写回这个档位。 -->
          <button type="button" class="ct-link" @click="overwrite(item)">{{ t('preset.overwrite') }}</button>
          <button type="button" class="ct-link" @click="startCopy(item)">{{ t('preset.copy') }}</button>
          <button type="button" class="ct-link" @click="startRename(item)">
            {{ t('common.rename') }}
          </button>
          <button
            type="button"
            class="ct-link"
            :disabled="item.id === presets.defaultID"
            @click="presets.setDefault(item.id)"
          >
            {{ t('preset.setDefault') }}
          </button>
          <button type="button" class="ct-link danger-link" @click="deletingID = item.id">
            {{ t('common.delete') }}
          </button>
        </li>
      </ul>

      <div v-if="form !== null" class="row-form">
        <input
          v-model="form.name"
          class="ct-input"
          type="text"
          :placeholder="t('preset.namePlaceholder')"
          @keyup.enter="confirmForm"
          @keyup.esc="form = null"
        />
        <input
          v-model="form.note"
          class="ct-input note"
          type="text"
          :placeholder="t('preset.notePlaceholder')"
          @keyup.enter="confirmForm"
          @keyup.esc="form = null"
        />
        <button type="button" class="ct-btn ct-btn--primary" @click="confirmForm">{{ t('common.save') }}</button>
        <button type="button" class="ct-btn" @click="form = null">{{ t('common.cancel') }}</button>
      </div>

      <div v-if="deletingID !== ''" class="row-form">
        <span class="ct-subtle">{{ t('preset.deleteConfirm', { name: deletingName }) }}</span>
        <button type="button" class="ct-btn danger-btn" @click="confirmDelete">{{ t('common.confirm') }}</button>
        <button type="button" class="ct-btn" @click="deletingID = ''">{{ t('common.cancel') }}</button>
      </div>

      <div class="io">
        <button type="button" class="ct-link" :disabled="rows.length === 0" @click="doExport">
          {{ t('preset.export') }}
        </button>
        <label class="ct-link file">
          {{ t('preset.import') }}
          <input type="file" accept="application/json,.json" hidden @change="doImport" />
        </label>
      </div>
    </template>
  </div>
</template>

<style scoped>
.manager {
  margin-top: var(--space-3);
  padding-top: var(--space-3);
  border-top: 1px solid var(--color-border);
  font-size: var(--font-size-sm);
}

.disclosure {
  font-size: var(--font-size-sm);
}

.rows {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: var(--space-2) 0 0;
  padding: 0;
  list-style: none;
}

.rows li {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}

.name {
  flex: 1;
  min-width: 120px;
  overflow-wrap: anywhere;
}

.badge {
  margin-left: var(--space-2);
  padding: 0 var(--space-2);
  border: 1px solid var(--color-primary);
  border-radius: var(--radius-pill);
  color: var(--color-primary-text);
  font-size: var(--font-size-xs);
}

.danger-link {
  color: var(--color-bad);
}

.row-form {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin-top: var(--space-2);
}

.row-form .ct-input {
  flex: 1;
  min-width: 140px;
}

/* 备注比名字长，给它多一点宽度。 */
.row-form .ct-input.note {
  flex: 1.4;
}

.danger-btn {
  border-color: var(--color-bad);
  color: var(--color-bad);
}

.io {
  display: flex;
  gap: var(--space-3);
  margin-top: var(--space-3);
}

.file {
  cursor: pointer;
}

button:disabled {
  opacity: 0.45;
  cursor: default;
}
</style>
