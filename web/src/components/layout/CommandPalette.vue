<script setup lang="ts">
/**
 * 命令面板。
 *
 * 存在的理由是「不想用鼠标找东西」：熟练用户想跳页、想切档位、想开始扫描时，
 * 手不必离开键盘。因此它的内容不是「所有功能的清单」，而是**最常用的动作**，
 * 并且按用途分组——列表长了就退化成又一个需要找东西的界面。
 *
 * 键盘：↑↓ 选择、Enter 执行、Esc 关闭。
 */
import { computed, nextTick, ref, watch } from 'vue'

import { sendCommand } from '@/api/client'
import { t } from '@/i18n'
import { usePresetsStore } from '@/stores/presets'
import { useActionStore } from '@/stores/actions'
import { useResultsStore } from '@/stores/results'
import { useTaskStore } from '@/stores/task'
import { useUIStore } from '@/stores/ui'

const open = defineModel<boolean>({ required: true })

const ui = useUIStore()
const task = useTaskStore()
const results = useResultsStore()
const actions = useActionStore()
const presets = usePresetsStore()

interface Command {
  id: string
  group: string
  label: string
  hint?: string
  run: () => void
}

const query = ref('')
const cursor = ref(0)
const input = ref<HTMLInputElement | null>(null)

/** 复制当前结果的全部地址，与工具栏「复制全部」同一份数据。 */
async function copyAll(): Promise<void> {
  const text = results.visible.map((record) => `${record.ip}:${record.port}`).join('\n')
  if (text === '') return
  try {
    await navigator.clipboard.writeText(text)
    ui.pushToast({ kind: 'ok', message: t('common.copied') })
  } catch {
    ui.pushToast({ kind: 'warn', message: t('common.copy') })
  }
}

function cycleTheme(): void {
  const order = ['system', 'dark', 'light'] as const
  const index = order.indexOf(ui.theme)
  ui.setTheme(order[(index + 1) % order.length] ?? 'system')
}

const commands = computed<Command[]>(() => {
  const pages: Command[] = [
    { id: 'go-scan', group: 'nav', label: t('nav.scan'), run: () => (ui.activeView = 'scan') },
    { id: 'go-result', group: 'nav', label: t('nav.result'), run: () => (ui.activeView = 'result') },
    { id: 'go-history', group: 'nav', label: t('nav.history'), run: () => (ui.activeView = 'history') },
    { id: 'go-settings', group: 'nav', label: t('nav.settings'), run: () => (ui.activeView = 'settings') },
  ]

  const operations: Command[] = [
    {
      id: 'scan-start',
      group: 'action',
      label: t('scan.start'),
      hint: 'Ctrl + Enter',
      // 走扫描页注册的动作，而不是自己拼一份参数——否则快捷键启动的任务
      // 会丢掉用户在扫描页选的来源与档位。
      run: () => {
        ui.activeView = 'scan'
        if (!actions.runStartScan()) {
          ui.pushToast({ kind: 'warn', message: t('common.loading') })
        }
      },
    },
    {
      id: 'scan-stop',
      group: 'action',
      label: t('scan.stop'),
      hint: 'Esc',
      run: () => sendCommand('scan/stop'),
    },
    { id: 'copy-all', group: 'action', label: t('result.copyAll'), run: () => void copyAll() },
    { id: 'theme', group: 'action', label: t('theme.label'), run: cycleTheme },
    {
      id: 'density',
      group: 'action',
      label: ui.showAdvanced ? t('density.simple') : t('density.advanced'),
      hint: 'Ctrl + Shift + A',
      run: () => ui.setDensity(ui.showAdvanced ? 'simple' : 'advanced'),
    },
    {
      id: 'contrast',
      group: 'action',
      label: t('contrast.label'),
      run: () => ui.setContrast(!ui.contrast),
    },
    { id: 'geo-update', group: 'action', label: t('geo.update'), run: () => sendCommand('geo/update') },
    { id: 'health', group: 'action', label: t('health.run'), run: () => sendCommand('health/check') },
  ]

  // 档位命令直接应用档位：命令面板的价值就是「不离开键盘把事办了」，只跳过去
  // 还得再点一下就没意义了。
  const presetCommands: Command[] = presets.list.map((preset) => ({
    id: `preset-${preset.id}`,
    group: 'preset',
    label: preset.name,
    run: () => {
      ui.activeView = 'scan'
      presets.use(preset.id)
    },
  }))

  return [...pages, ...operations, ...presetCommands]
})

/** 匹配：标签里包含关键词即可。刻意不做模糊打分——命令总共十几条，打分只会让顺序变得难以预期。 */
const matched = computed(() => {
  const text = query.value.trim().toLowerCase()
  if (text === '') return commands.value
  return commands.value.filter((command) => command.label.toLowerCase().includes(text))
})

const groups = computed(() => {
  const order = ['nav', 'action', 'preset']
  return order
    .map((id) => ({ id, items: matched.value.filter((command) => command.group === id) }))
    .filter((group) => group.items.length > 0)
})

/** 扁平顺序，供上下键遍历：分组只影响显示，不影响选择。 */
const flat = computed(() => groups.value.flatMap((group) => group.items))

watch(open, async (isOpen) => {
  if (!isOpen) return
  query.value = ''
  cursor.value = 0
  await nextTick()
  input.value?.focus()
})

watch(matched, () => {
  // 结果变了就把光标收回第一项，否则会停在一个已经不存在的下标上。
  cursor.value = 0
})

function run(command: Command): void {
  open.value = false
  command.run()
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    cursor.value = Math.min(cursor.value + 1, flat.value.length - 1)
    return
  }
  if (event.key === 'ArrowUp') {
    event.preventDefault()
    cursor.value = Math.max(cursor.value - 1, 0)
    return
  }
  if (event.key === 'Enter') {
    event.preventDefault()
    const command = flat.value[cursor.value]
    if (command) run(command)
  }
}

/** 分组标题。 */
function groupLabel(id: string): string {
  if (id === 'nav') return t('nav.command')
  if (id === 'action') return t('common.more')
  return t('task.preset')
}
</script>

<template>
  <div v-if="open" class="scrim" @click.self="open = false">
    <div class="palette" role="dialog" aria-modal="true" :aria-label="t('nav.command')">
      <input
        ref="input"
        v-model="query"
        class="query"
        type="text"
        :placeholder="t('nav.command')"
        @keydown="onKeydown"
        @keydown.esc="open = false"
      />

      <p v-if="flat.length === 0" class="ct-subtle empty">{{ t('common.empty') }}</p>

      <div v-else class="list">
        <template v-for="group in groups" :key="group.id">
          <div class="group-label">{{ groupLabel(group.id) }}</div>
          <button
            v-for="command in group.items"
            :key="command.id"
            type="button"
            class="row"
            :class="{ on: flat[cursor]?.id === command.id }"
            @mouseenter="cursor = flat.findIndex((item) => item.id === command.id)"
            @click="run(command)"
          >
            <span>{{ command.label }}</span>
            <span class="spacer" />
            <kbd v-if="command.hint">{{ command.hint }}</kbd>
          </button>
        </template>
      </div>

      <div class="foot ct-subtle">
        <span>↑↓ 选择</span>
        <span>Enter 执行</span>
        <span>Esc 关闭</span>
        <span class="spacer" />
        <span v-if="task.running">{{ t('task.running') }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.scrim {
  position: fixed;
  inset: 0;
  z-index: var(--z-dialog);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 12vh var(--space-4) var(--space-4);
  background: rgb(15 23 42 / 42%);
}

.palette {
  width: 100%;
  max-width: 520px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  background: var(--color-surface-raised);
  box-shadow: var(--elevation-overlay);
  overflow: hidden;
}

.query {
  width: 100%;
  height: 46px;
  padding: 0 var(--space-4);
  border: 0;
  border-bottom: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-text);
  font-size: var(--font-size-md);
}

.query:focus {
  outline: none;
}

.list {
  max-height: 46vh;
  overflow-y: auto;
  padding: var(--space-2) 0;
}

.group-label {
  padding: var(--space-2) var(--space-4) var(--space-1);
  color: var(--color-text-subtle);
  font-size: var(--font-size-xs);
}

.row {
  display: flex;
  width: 100%;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-4);
  border: 0;
  background: transparent;
  color: var(--color-text);
  font-size: var(--font-size-sm);
  text-align: left;
  cursor: pointer;
}

.row.on {
  background: var(--color-primary-soft);
  color: var(--color-primary-text);
}

.spacer {
  flex: 1;
}

.empty {
  padding: var(--space-5) var(--space-4);
  text-align: center;
}

.foot {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-4);
  border-top: 1px solid var(--color-border);
  font-size: var(--font-size-xs);
}
</style>
