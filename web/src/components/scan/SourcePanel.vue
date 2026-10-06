<script setup lang="ts">
/**
 * 来源面板：四种来源各自有位置。
 *
 * 刻意不做成「一个输入框 + 一个模式下拉」：来源是这个工具最要紧的输入，
 * 用户必须能一眼看到「我到底在扫什么」。四种来源的形态差别很大——开关、
 * 列表、文本、文件——用同一个控件装它们，等于把区别藏起来。
 *
 * 本地文件读进来的内容填进自定义文本区，不做「隐藏的第二个来源」：
 * 用户读完之后仍然能看见、能改。
 */
import { computed, ref } from 'vue'

import { t } from '@/i18n'
import { useUIStore } from '@/stores/ui'
import { parseSourceText } from '@/utils/sourceText'

export interface RemoteSource {
  url: string
  enabled: boolean
  note: string
}

export interface ScanSource {
  official: boolean
  remote: RemoteSource[]
  customText: string
}

const ui = useUIStore()

const source = defineModel<ScanSource>({ required: true })

defineProps<{
  /** 官方网段条数，由后端下发，前端不硬编码。 */
  officialCount: number
}>()

const fileInput = ref<HTMLInputElement | null>(null)
const dragging = ref(false)

const preview = computed(() => parseSourceText(source.value.customText))
const invalidCount = computed(() => preview.value.invalid.length)

function addRemote(): void {
  source.value = {
    ...source.value,
    remote: [...source.value.remote, { url: '', enabled: true, note: '' }],
  }
}

function updateRemote(index: number, patch: Partial<RemoteSource>): void {
  source.value = {
    ...source.value,
    remote: source.value.remote.map((item, i) => (i === index ? { ...item, ...patch } : item)),
  }
}

function removeRemote(index: number): void {
  source.value = { ...source.value, remote: source.value.remote.filter((_, i) => i !== index) }
}

/** 把文件内容追加到自定义文本区：追加而不是覆盖，用户可能已经写了一部分。 */
function appendText(text: string): void {
  const current = source.value.customText.trimEnd()
  source.value = {
    ...source.value,
    customText: current === '' ? text : `${current}\n${text}`,
  }
}

async function readFiles(files: FileList | null): Promise<void> {
  if (!files) return
  for (const file of Array.from(files)) {
    appendText(await file.text())
  }
}

async function onDrop(event: DragEvent): Promise<void> {
  dragging.value = false
  await readFiles(event.dataTransfer?.files ?? null)
}
</script>

<template>
  <section class="ct-card">
    <h2 class="ct-card-title">{{ t('source.title') }}</h2>

    <!-- ① 官方网段 -->
    <label class="row">
      <input v-model="source.official" type="checkbox" class="ct-check" />
      <span class="row-text">
        <span>{{ t('source.official') }}</span>
        <span class="ct-subtle">{{ t('source.official.desc') }}</span>
      </span>
      <span class="ct-subtle tnum">{{ t('source.official.count', { count: officialCount }) }}</span>
    </label>

    <!--
      远端地址、自定义文本、本地文件三块收进「详细设置」：
      简单模式下只留「官方网段」这一个开关——它覆盖绝大多数场景，而这三块
      是给有特定来源的人用的。
    -->
    <template v-if="ui.showAdvanced">
    <!-- ② 远端地址 -->
    <div class="block">
      <div class="block-head">
        <span>{{ t('source.remote') }}</span>
        <span class="ct-subtle">{{ t('source.remote.desc') }}</span>
        <span class="spacer" />
        <button type="button" class="ct-link" @click="addRemote">{{ t('source.remote.add') }}</button>
      </div>
      <p v-if="source.remote.length === 0" class="ct-subtle empty">{{ t('source.remote.empty') }}</p>
      <div v-for="(item, index) in source.remote" :key="index" class="remote-row">
        <input
          :checked="item.enabled"
          type="checkbox"
          class="ct-check"
          :aria-label="t('source.remote')"
          @change="updateRemote(index, { enabled: ($event.target as HTMLInputElement).checked })"
        />
        <input
          :value="item.url"
          class="ct-input ct-mono"
          placeholder="https://example.com/list.txt"
          @input="updateRemote(index, { url: ($event.target as HTMLInputElement).value })"
        />
        <input
          :value="item.note"
          class="ct-input note"
          :placeholder="t('source.remote.note')"
          @input="updateRemote(index, { note: ($event.target as HTMLInputElement).value })"
        />
        <button type="button" class="ct-link" @click="removeRemote(index)">
          {{ t('source.remote.remove') }}
        </button>
      </div>
      <p v-if="source.remote.length > 0" class="ct-subtle hint">{{ t('scan.remoteNote') }}</p>
    </div>

    <!-- ③ 自定义文本 -->
    <div class="block">
      <div class="block-head">
        <span>{{ t('source.text') }}</span>
        <span class="ct-subtle">{{ t('source.text.desc') }}</span>
      </div>
      <textarea
        v-model="source.customText"
        class="ct-textarea ct-mono"
        rows="6"
        spellcheck="false"
        :placeholder="t('source.text.placeholder')"
      />
      <div class="preview">
        <span :class="invalidCount > 0 ? 'warn' : 'ok'">
          {{ t('source.preview', { cidrs: preview.cidrs, single: preview.single, hosts: preview.hosts, comments: preview.comments }) }}
        </span>
        <span v-if="invalidCount > 0" class="warn">
          · {{ t('source.preview.invalid', { count: invalidCount }) }}
        </span>
      </div>
    </div>

    <!-- ④ 本地文件 -->
    <div class="block">
      <div class="block-head">
        <span>{{ t('source.file') }}</span>
        <span class="ct-subtle">{{ t('source.file.hint') }}</span>
      </div>
      <div
        class="ct-drop"
        :class="{ 'ct-drop--active': dragging }"
        @dragover.prevent="dragging = true"
        @dragleave="dragging = false"
        @drop.prevent="onDrop"
      >
        <span>{{ t('source.file.drop') }}</span>
        <button type="button" class="ct-btn" @click="fileInput?.click()">{{ t('source.file.pick') }}</button>
        <input
          ref="fileInput"
          type="file"
          multiple
          accept=".txt,.csv,.json,.list,text/plain"
          class="hidden-input"
          @change="readFiles(($event.target as HTMLInputElement).files)"
        />
      </div>
    </div>
    </template>
  </section>
</template>

<style scoped>
.row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-surface-sunken);
  cursor: pointer;
}

.row-text {
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.spacer {
  flex: 1;
}

.block {
  margin-top: var(--space-4);
}

.block-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
  font-size: var(--font-size-sm);
}

.empty,
.hint {
  margin-top: var(--space-1);
}

.remote-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
}

.remote-row .ct-input {
  flex: 1;
}

.note {
  flex: 0 0 28%;
}

.preview {
  display: flex;
  gap: var(--space-2);
  margin-top: var(--space-2);
  font-size: var(--font-size-xs);
}

.ok {
  color: var(--color-ok);
}

.warn {
  color: var(--color-warn);
}

.hidden-input {
  display: none;
}
</style>
