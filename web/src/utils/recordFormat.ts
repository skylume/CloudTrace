/**
 * 记录字段的渲染描述。
 *
 * 字段的**存在与含义**由后端下发（`GET /api/export/fields`），这里只描述
 * 「怎么显示」——对齐方式、是否等宽、是否用信号条、单位是什么。
 *
 * 分开的理由：后端加一个字段，前端不改一行就能显示出来（走默认渲染）；
 * 而渲染方式属于纯前端的表达，后端不该关心。
 *
 * 注意这里**没有字段清单本身**：清单来自后端，写死在前后端任何一处都会漂移。
 */
import { formatLatency, formatLoss, formatSpeed } from './latency'

export type FieldRender =
  | 'text'
  | 'mono'
  | 'number'
  | 'latency'
  | 'speed'
  | 'loss'
  | 'percent'
  | 'bool'
  | 'json'

export interface RenderSpec {
  render: FieldRender
  /** 数值列右对齐，文字列左对齐。 */
  align: 'left' | 'right'
  /** 列宽提示，单位 px。0 表示自适应。 */
  width?: number
  unit?: string
}

/**
 * 每个字段的渲染方式。
 *
 * 没列在这里的字段走默认（左对齐文本）——后端新增字段时不会因为前端没登记
 * 就显示不出来，只是显示得朴素一点。
 */
const SPECS: Record<string, RenderSpec> = {
  ip: { render: 'mono', align: 'left', width: 168 },
  port: { render: 'mono', align: 'right', width: 64 },
  use_tls: { render: 'bool', align: 'left', width: 56 },
  latency: { render: 'latency', align: 'left', width: 128 },
  latency_avg: { render: 'number', align: 'right', unit: 'ms' },
  latency_max: { render: 'number', align: 'right', unit: 'ms' },
  jitter: { render: 'number', align: 'right', unit: 'ms' },
  loss: { render: 'loss', align: 'right', width: 64 },
  sent: { render: 'number', align: 'right', width: 56 },
  recv: { render: 'number', align: 'right', width: 56 },
  colo: { render: 'mono', align: 'left', width: 64 },
  region_name: { render: 'text', align: 'left', width: 96 },
  loc: { render: 'mono', align: 'left', width: 56 },
  asn: { render: 'mono', align: 'right', width: 84 },
  as_org: { render: 'text', align: 'left', width: 180 },
  geo_warn: { render: 'text', align: 'left' },
  speed_mbps: { render: 'speed', align: 'left', width: 128 },
  score: { render: 'number', align: 'right', width: 64 },
  trace: { render: 'json', align: 'left' },
}

/** 取某个字段的渲染描述；未登记时给一份安全的默认值。 */
export function renderSpec(key: string): RenderSpec {
  return SPECS[key] ?? { render: 'text', align: 'left' }
}

/**
 * 把字段值格式化成可显示的文字。
 *
 * 未采集的字段一律给破折号，**绝不显示 0**——0 是合法值（延迟可以是 0ms、
 * 丢包可以是 0%），用它表示「没有数据」会让人读错。
 */
export function formatField(key: string, value: unknown, record?: { sent?: number }): string {
  if (value === null || value === undefined || value === '') return '—'
  const spec = renderSpec(key)

  switch (spec.render) {
    case 'latency':
      return formatLatency(Number(value))
    case 'speed':
      return formatSpeed(Number(value))
    case 'loss':
      return formatLoss(Number(value), record?.sent ?? 0)
    case 'percent':
      return `${Math.round(Number(value) * 100)}%`
    case 'bool':
      // 用符号而不是「是/否」：符号不需要翻译，也不会在窄列里被截断。
      return value ? '✓' : '—'
    case 'json':
      return '—'
    case 'number': {
      if (typeof value !== 'number') return String(value)
      // 整数不带小数点，小数保留一位：表格里一列数字的小数位必须一致，
      // 否则扫读时会一直被打断。
      const text = Number.isInteger(value) ? String(value) : value.toFixed(1)
      return spec.unit ? `${text} ${spec.unit}` : text
    }
    default:
      return String(value)
  }
}
