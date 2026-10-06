/**
 * 结果表的右键菜单。
 *
 * 复制是拿到结果后最高频的动作，而表格里的复制入口原来只有「双击单元格」——
 * 那一个不够用：用户想要的是「这一行」「这几行」「只留 ip:port」。
 *
 * 抽出来是因为它要拼三种不同的文本、还要管菜单的开关与位置，和表格的排序、
 * 分组、展开放在一起会让那一个文件同时讲四件事。
 */
import { computed, ref } from 'vue'

import type { ContextMenuItem } from '@/components/ui/contextMenu'
import { t } from '@/i18n'
import type { FieldDef, IPRecord } from '@/api/types'
import { useResultsStore } from '@/stores/results'
import { useUIStore } from '@/stores/ui'
import { formatField } from '@/utils/recordFormat'

interface MenuState {
  x: number
  y: number
  record: IPRecord
}

export function useRowContextMenu(columns: () => FieldDef[]) {
  const results = useResultsStore()
  const ui = useUIStore()
  const menu = ref<MenuState | null>(null)

  function open(record: IPRecord, event: MouseEvent): void {
    menu.value = { x: event.clientX, y: event.clientY, record }
  }

  function close(): void {
    menu.value = null
  }

  /** 复制若干行，每行 `ip:port`。 */
  async function copyAddresses(records: IPRecord[]): Promise<void> {
    const text = records.map((record) => `${record.ip}:${record.port}`).join('\n')
    if (text === '') return
    try {
      await navigator.clipboard.writeText(text)
      ui.pushToast({ kind: 'ok', message: t('common.copied') })
    } catch {
      // 剪贴板在非安全上下文里不可用；此时提示用户手动选，不要静默失败。
      ui.pushToast({ kind: 'warn', message: t('common.copy') })
    }
  }

  /** 复制整行：用户右键的往往是想粘到别处当备注，带上字段名更好认。 */
  async function copyRow(record: IPRecord): Promise<void> {
    const values = record as unknown as Record<string, unknown>
    const parts = columns()
      .map((column) => `${column.label} ${formatField(column.key, values[column.key], record)}`)
      .join('  ')
    try {
      await navigator.clipboard.writeText(`${record.ip}:${record.port}  ${parts}`)
      ui.pushToast({ kind: 'ok', message: t('common.copied') })
    } catch {
      ui.pushToast({ kind: 'warn', message: t('common.copy') })
    }
  }

  const items = computed<ContextMenuItem[]>(() => {
    const record = menu.value?.record
    if (!record) return []
    const selected = results.selectedRecords
    return [
      { label: t('result.copyRow'), action: () => void copyRow(record) },
      {
        label: t('result.copySelectedRows', { count: selected.length }),
        action: () => void copyAddresses(selected),
        disabled: selected.length === 0,
      },
      { label: t('result.copyAddress'), action: () => void copyAddresses([record]) },
    ]
  })

  return { menu, items, open, close }
}
