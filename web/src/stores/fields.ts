/**
 * 字段清单与列预设。
 *
 * 字段来自后端 `GET /api/export/fields`——表格列、导出字段、行内详情共用
 * 同一份清单。前端只决定「默认显示哪几列」，字段本身的存在与含义由后端决定：
 * 后端加一个字段，前端不改一行就能在行内详情里看到它。
 *
 * 这也是「列多 ≠ 文件长」的前提：模板里只有一层 `v-for`，从 8 列加到 19 列
 * 模板一行都不会变。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { api } from '@/api/rest'
import type { FieldDef } from '@/api/types'

/** 列预设：三档，从「只想挑一个能用就走」到「排查线路问题」。 */
export type ColumnPresetId = 'brief' | 'standard' | 'full'

export interface ColumnPreset {
  id: ColumnPresetId
  /** 预设名走 i18n，这里只存键。 */
  labelKey: string
  keys: string[]
}

/**
 * 三档列预设。
 *
 * `sent` / `recv` / `loc` / `trace` 不进预设：前三个是过程量，`trace` 体量大，
 * 它们放进行内展开更合适——列宽是稀缺资源，展开是免费的。
 */
export const COLUMN_PRESETS: ColumnPreset[] = [
  {
    id: 'brief',
    labelKey: 'columns.brief',
    keys: ['ip', 'region_name', 'latency', 'loss', 'speed_mbps', 'score'],
  },
  {
    id: 'standard',
    labelKey: 'columns.standard',
    keys: ['ip', 'region_name', 'colo', 'latency', 'latency_avg', 'jitter', 'loss', 'use_tls', 'speed_mbps', 'score'],
  },
  {
    id: 'full',
    labelKey: 'columns.full',
    keys: [
      'ip',
      'region_name',
      'colo',
      'latency',
      'latency_avg',
      'latency_max',
      'jitter',
      'loss',
      'use_tls',
      'speed_mbps',
      'score',
      'asn',
      'as_org',
      'geo_warn',
    ],
  },
]

/** 列设置里可以勾选的键：预设里出现过的都算，其余靠「全部字段」补齐。 */
export const DEFAULT_PRESET: ColumnPresetId = 'standard'

/** 列宽的本地存储键。与界面偏好分开存：它是表格自己的事。 */
const WIDTHS_KEY = 'cloudtrace.columnWidths'

/** 可见列与预设的本地存储键。 */
const COLUMNS_KEY = 'cloudtrace.columns'

/** 列宽下限。再窄就看不出这一列是什么了。 */
const MIN_COLUMN_WIDTH = 56

/**
 * 读回用户拖过的列宽。
 *
 * 只接受认得出的数字：这份记忆只是省一次拖动，里面混进一个坏值就让整张表
 * 排版错乱的话，代价远大于收益。
 */
function loadWidths(): Record<string, number> {
  try {
    const raw = localStorage.getItem(WIDTHS_KEY)
    if (!raw) return {}
    const parsed = JSON.parse(raw) as Record<string, unknown>
    const out: Record<string, number> = {}
    for (const [key, value] of Object.entries(parsed)) {
      if (typeof value === 'number' && Number.isFinite(value) && value >= MIN_COLUMN_WIDTH) {
        out[key] = value
      }
    }
    return out
  } catch {
    return {}
  }
}

/** 读回用户选过的列与档位。 */
function loadColumns(): { presetId: ColumnPresetId | 'custom'; visibleKeys: string[] } {
  const fallback = { presetId: DEFAULT_PRESET as ColumnPresetId | 'custom', visibleKeys: presetKeys(DEFAULT_PRESET) }
  try {
    const raw = localStorage.getItem(COLUMNS_KEY)
    if (!raw) return fallback
    const parsed = JSON.parse(raw) as { presetId?: unknown; visibleKeys?: unknown }
    const keys = Array.isArray(parsed.visibleKeys)
      ? parsed.visibleKeys.filter((item): item is string => typeof item === 'string')
      : []
    // 一列都不剩说明这份记忆坏了：空表格比「回到默认」更难解释。
    if (keys.length === 0) return fallback
    const preset = parsed.presetId
    return {
      presetId: preset === 'brief' || preset === 'standard' || preset === 'full' || preset === 'custom' ? preset : 'custom',
      visibleKeys: keys,
    }
  } catch {
    return fallback
  }
}

function saveColumns(presetId: ColumnPresetId | 'custom', visibleKeys: string[]): void {
  try {
    localStorage.setItem(COLUMNS_KEY, JSON.stringify({ presetId, visibleKeys }))
  } catch {
    /* 隐私模式下写不进去，忽略即可 */
  }
}

export const useFieldsStore = defineStore('fields', () => {
  const fields = ref<FieldDef[]>([])
  const loading = ref(false)
  const error = ref('')

  const stored = loadColumns()

  /** 当前生效的可见列（按显示顺序）。 */
  const visibleKeys = ref<string[]>(stored.visibleKeys)
  const presetId = ref<ColumnPresetId | 'custom'>(stored.presetId)

  /**
   * 用户拖出来的列宽，按字段键存。
   *
   * 没记录过的列返回空字符串，交给浏览器按内容撑开——给十九个字段各写一个
   * 拍脑袋的默认值，只会让「默认就不好看」变成常态。
   */
  const widths = ref<Record<string, number>>(loadWidths())

  function widthOf(key: string): string {
    const px = widths.value[key]
    return px ? `${px}px` : ''
  }

  function setWidth(key: string, px: number): void {
    widths.value = { ...widths.value, [key]: Math.max(MIN_COLUMN_WIDTH, Math.round(px)) }
    try {
      localStorage.setItem(WIDTHS_KEY, JSON.stringify(widths.value))
    } catch {
      /* 隐私模式下写不进去，忽略即可 */
    }
  }

  /** 清掉全部自定义列宽，回到「按内容撑开」。 */
  function resetWidths(): void {
    widths.value = {}
    try {
      localStorage.removeItem(WIDTHS_KEY)
    } catch {
      /* 同上 */
    }
  }

  /** 按 key 快速取字段定义，渲染时用。 */
  const byKey = computed(() => {
    const map = new Map<string, FieldDef>()
    for (const field of fields.value) map.set(field.key, field)
    return map
  })

  /** 可见列对应的字段定义。后端没下发某个键时跳过，不让表格崩掉。 */
  const columns = computed(() =>
    visibleKeys.value.map((key) => byKey.value.get(key)).filter((field): field is FieldDef => Boolean(field)),
  )

  async function load(): Promise<void> {
    if (fields.value.length > 0 || loading.value) return
    loading.value = true
    try {
      const data = await api.exportFields()
      fields.value = data.fields
      error.value = ''
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err)
    } finally {
      loading.value = false
    }
  }

  function applyPreset(id: ColumnPresetId): void {
    presetId.value = id
    visibleKeys.value = presetKeys(id)
    saveColumns(presetId.value, visibleKeys.value)
  }

  /** 手动改列：与任何预设都不一致时记为「自定义」。 */
  function setVisibleKeys(keys: string[]): void {
    visibleKeys.value = keys
    const matched = COLUMN_PRESETS.find(
      (preset) => preset.keys.length === keys.length && preset.keys.every((key) => keys.includes(key)),
    )
    presetId.value = matched ? matched.id : 'custom'
    saveColumns(presetId.value, keys)
  }

  function toggleKey(key: string): void {
    const next = visibleKeys.value.includes(key)
      ? visibleKeys.value.filter((item) => item !== key)
      : [...visibleKeys.value, key]
    // 一列都不剩时不给通过：空表格比缺一列更难理解。
    if (next.length === 0) return
    setVisibleKeys(next)
  }

  /**
   * 把某一列在可见顺序里前后挪一格。
   *
   * 用「上移 / 下移」而不是拖拽：列数最多十几个，两个按钮就能到任何位置，
   * 而且键盘也能操作——拖拽排序在键盘上等于没有。
   */
  function moveKey(key: string, delta: number): void {
    const keys = [...visibleKeys.value]
    const from = keys.indexOf(key)
    if (from < 0) return
    const to = from + delta
    if (to < 0 || to >= keys.length) return
    keys.splice(to, 0, ...keys.splice(from, 1))
    setVisibleKeys(keys)
  }

  return {
    fields,
    loading,
    error,
    byKey,
    columns,
    visibleKeys,
    presetId,
    widths,
    widthOf,
    setWidth,
    resetWidths,
    load,
    applyPreset,
    setVisibleKeys,
    toggleKey,
    moveKey,
  }
})

function presetKeys(id: ColumnPresetId): string[] {
  return COLUMN_PRESETS.find((preset) => preset.id === id)?.keys ?? []
}
