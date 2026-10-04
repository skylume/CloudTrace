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
import { usePresetsStore } from '@/stores/presets'
import { useUIStore } from '@/stores/ui'
import { exportPresets, parsePresets } from '@/utils/presetIO'

const presets = usePresetsStore()
const ui = useUIStore()

const open = ref(false)
/** 正在重命名的档位标识；空表示没有在改。 */
const renamingID = ref('')
const draftName = ref('')
/** 等待确认删除的档位标识。删除不可恢复，先问一次。 */
const deletingID = ref('')

const rows = computed(() => presets.custom)
const deletingName = computed(() => presets.byID(deletingID.value)?.name ?? '')

function startRename(id: string, name: string): void {
  renamingID.value = id
  draftName.value = name
  deletingID.value = ''
}

function confirmRename(): void {
  if (presets.rename(renamingID.value, draftName.value)) {
    renamingID.value = ''
    draftName.value = ''
  }
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
          <button type="button" class="ct-link" @click="startRename(item.id, item.name)">
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

      <div v-if="renamingID !== ''" class="row-form">
        <input
          v-model="draftName"
          class="ct-input"
          type="text"
          :placeholder="t('preset.namePlaceholder')"
          @keyup.enter="confirmRename"
          @keyup.esc="renamingID = ''"
        />
        <button type="button" class="ct-btn ct-btn--primary" @click="confirmRename">{{ t('common.save') }}</button>
        <button type="button" class="ct-btn" @click="renamingID = ''">{{ t('common.cancel') }}</button>
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
  min-width: 160px;
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
