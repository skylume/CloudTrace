/**
 * 档位。
 *
 * 档位的权威在后端：内置三档由后端定义、自定义档位由后端存盘，前端只负责展示
 * 与转发命令。前端留一份常量表看起来省事，但两边一定会分叉——改了后端的内置
 * 档位，前端那份拷贝还在用旧值，而用户看到的正是前端那份。
 *
 * 与设置同样的约定：全量替换，不做增量合并。后端的 `presets` 事件既是一次读取
 * 的结果，也是任何一次变更的广播，两者处理方式完全一样。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { CMD } from '@/api/protocol'
import { sendCommand } from '@/api/client'
import type { Preset, PresetsPayload } from '@/api/types'
import { uniquePresetID } from '@/utils/presetIO'

/** 自定义档位的标识前缀由后端生成，前端只负责拼接一个不会撞车的值。 */
function newPresetID(): string {
  return `my-${Date.now().toString(36)}`
}

/**
 * 算出上移 / 下移之后各档位的排序号。
 *
 * 只返回顺序真的变了的那些：每个都要走一次保存，没动的不用重发。
 *
 * 按「位置」而不是「交换两个 Order 值」来算：两个档位的 Order 可能相同（都
 * 是新存的，默认 0），交换一个不变的值等于没动。
 */
export function reorderPlan(items: Preset[], id: string, delta: number): { id: string; order: number }[] {
  const from = items.findIndex((item) => item.id === id)
  const to = from + delta
  if (from < 0 || to < 0 || to >= items.length) return []

  const next = items.slice()
  const [moved] = next.splice(from, 1)
  if (moved === undefined) return []
  next.splice(to, 0, moved)

  const plan: { id: string; order: number }[] = []
  next.forEach((item, index) => {
    if (item.order !== index) plan.push({ id: item.id, order: index })
  })
  return plan
}

export const usePresetsStore = defineStore('presets', () => {
  const list = ref<Preset[]>([])
  const defaultID = ref('')
  const loaded = ref(false)

  /** 内置档位。界面按这个分组，与「我的档位」分开。 */
  const builtin = computed(() => list.value.filter((item) => item.builtin))
  const custom = computed(() => list.value.filter((item) => !item.builtin))

  function apply(payload: PresetsPayload): void {
    list.value = payload.presets ?? []
    defaultID.value = payload.default ?? ''
    loaded.value = true
  }

  function byID(id: string): Preset | undefined {
    return list.value.find((item) => item.id === id)
  }

  /** 拉一次全量档位。重连之后必须重新拉，断线期间的改动补不回来。 */
  function refresh(): void {
    sendCommand(CMD.presetsList)
  }

  /**
   * 应用一个档位。
   *
   * 只发标识，参数与来源标记都由后端算——「我的档位载入的值标 user」这条规则
   * 只该有一处实现，前端再算一遍迟早会与后端分叉。
   */
  function use(id: string): void {
    if (id === '') return
    sendCommand(CMD.presetsApply, { id })
  }

  /** 另存为一个新档位。values 的键是配置里的点号路径。 */
  function save(input: { id?: string; name: string; note?: string; values: Record<string, unknown> }): boolean {
    const name = input.name.trim()
    if (name === '') return false
    if (Object.keys(input.values).length === 0) return false
    return sendCommand(CMD.presetsSave, {
      id: input.id ?? newPresetID(),
      name,
      note: input.note ?? '',
      values: input.values,
    })
  }

  function remove(id: string): void {
    sendCommand(CMD.presetsDelete, { id })
  }

  function setDefault(id: string): void {
    sendCommand(CMD.presetsSetDefault, { id })
  }

  /** 改个名字。覆盖保存同一个标识即可，不必另设命令。 */
  function rename(id: string, name: string): boolean {
    const preset = byID(id)
    const trimmed = name.trim()
    if (!preset || trimmed === '') return false
    return sendCommand(CMD.presetsSave, { ...preset, name: trimmed })
  }

  /** 在「我的档位」里上移 / 下移。 */
  function move(id: string, delta: number): void {
    const items = custom.value
    for (const step of reorderPlan(items, id, delta)) {
      const preset = byID(step.id)
      if (preset) sendCommand(CMD.presetsSave, { ...preset, order: step.order })
    }
  }

  /**
   * 导入一批档位，返回实际发出的保存数。
   *
   * 每个都换一个新标识：文件是从别人那儿来的，沿用原标识会把本机同标识的
   * 档位顶掉——而用户导入的预期是「多几个档位」，不是「替换掉现在这个」。
   */
  function importMany(incoming: Preset[]): number {
    const taken = new Set(list.value.map((item) => item.id))
    let count = 0
    for (const item of incoming) {
      const id = uniquePresetID(item.id, taken)
      taken.add(id)
      if (sendCommand(CMD.presetsSave, { ...item, id })) count += 1
    }
    return count
  }

  return {
    list,
    defaultID,
    loaded,
    builtin,
    custom,
    byID,
    refresh,
    apply,
    use,
    save,
    remove,
    setDefault,
    rename,
    move,
    importMany,
  }
})
