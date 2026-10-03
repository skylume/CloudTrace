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

export const useFieldsStore = defineStore('fields', () => {
  const fields = ref<FieldDef[]>([])
  const loading = ref(false)
  const error = ref('')

  /** 当前生效的可见列（按显示顺序）。 */
  const visibleKeys = ref<string[]>(presetKeys(DEFAULT_PRESET))
  const presetId = ref<ColumnPresetId | 'custom'>(DEFAULT_PRESET)

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
  }

  /** 手动改列：与任何预设都不一致时记为「自定义」。 */
  function setVisibleKeys(keys: string[]): void {
    visibleKeys.value = keys
    const matched = COLUMN_PRESETS.find(
      (preset) => preset.keys.length === keys.length && preset.keys.every((key) => keys.includes(key)),
    )
    presetId.value = matched ? matched.id : 'custom'
  }

  function toggleKey(key: string): void {
    const next = visibleKeys.value.includes(key)
      ? visibleKeys.value.filter((item) => item !== key)
      : [...visibleKeys.value, key]
    // 一列都不剩时不给通过：空表格比缺一列更难理解。
    if (next.length === 0) return
    setVisibleKeys(next)
  }

  return { fields, loading, error, byKey, columns, visibleKeys, presetId, load, applyPreset, setVisibleKeys, toggleKey }
})

function presetKeys(id: ColumnPresetId): string[] {
  return COLUMN_PRESETS.find((preset) => preset.id === id)?.keys ?? []
}
