/**
 * 扫描参数的标签、取值范围与说明，以及档位与参数之间的换算。
 *
 * 参数的中文标签、取值范围、说明都集中在这里，组件只负责渲染——文案散在
 * 模板里就没法统一改，也没法做双语。
 *
 * 档位本身不在这里：内置档位与自定义档位都由后端定义并存盘（见 `stores/presets`）。
 * 前端留一份常量表看起来省事，但两边一定会分叉——改了后端的内置档位，前端那
 * 份拷贝还在用旧值，而用户看到的正是前端那份。这里只负责把后端的档位值换算成
 * 界面用的形状。
 */

export interface ParamSpec {
  /** 对应 ScanParams 的字段名。 */
  key: string
  labelKey: string
  hintKey: string
  kind: 'int' | 'bool'
  unit?: string
  min?: number
  max?: number
  /** 出现在档位参数摘要里；否则只在「全部参数」中显示。 */
  summary?: boolean
  /**
   * 建议区间。超出只标警示色，**不阻止填写**。
   *
   * 与 min/max 是两件事：min/max 是「填不进去」的硬边界，建议区间是「填了会
   * 有什么后果」。用户有权把自己网络的参数设成别人看来不合理的样子，但该
   * 知道代价是什么。
   */
  warnBelow?: number
  warnAbove?: number
}

/** 扫描参数清单。顺序就是界面上显示的顺序。 */
export const SCAN_PARAMS: ParamSpec[] = [
  {
    key: 'sampleMax',
    labelKey: 'param.scan.sample_max',
    hintKey: 'param.scan.sample_max.hint',
    kind: 'int',
    min: 0,
    max: 5000,
    summary: true,
  },
  {
    key: 'workers',
    labelKey: 'param.scan.workers',
    hintKey: 'param.scan.workers.hint',
    kind: 'int',
    min: 1,
    // 硬上限就是后端校验的上限。它比内置档位高得多是有意的：档位只到 200，
    // 但手填是高级选项，用户有权设得更大——该做的是给警示色，不是拦住他。
    max: 2000,
    summary: true,
    // 弱网与老路由上并发拉满会把自己的网络压垮，而结果反而更差。
    warnAbove: 300,
  },
  {
    key: 'latencyThreshold',
    labelKey: 'param.scan.latency_threshold',
    hintKey: 'param.scan.latency_threshold.hint',
    kind: 'int',
    unit: 'ms',
    min: 1,
    max: 5000,
    summary: true,
    // 阈值压到 100 以下，能通过的节点会少到几乎没有，白等一轮。
    warnBelow: 100,
  },
  {
    key: 'pingTimes',
    labelKey: 'param.scan.ping_times',
    hintKey: 'param.scan.ping_times.hint',
    kind: 'int',
    min: 0,
    max: 20,
    summary: true,
    // 每轮多测几次更准，但时间成倍增长。
    warnAbove: 5,
  },
  {
    key: 'port',
    labelKey: 'param.scan.port',
    hintKey: 'param.scan.port.hint',
    kind: 'int',
    min: 1,
    max: 65535,
    summary: true,
  },
  {
    key: 'timeoutMs',
    labelKey: 'param.scan.timeout_ms',
    hintKey: 'param.scan.timeout_ms.hint',
    kind: 'int',
    unit: 'ms',
    min: 100,
    max: 30000,
  },
  {
    key: 'retry',
    labelKey: 'param.scan.retry',
    hintKey: 'param.scan.retry.hint',
    kind: 'int',
    min: 0,
    max: 10,
  },
  {
    key: 'twoPhase',
    labelKey: 'param.scan.two_phase',
    hintKey: 'param.scan.two_phase.hint',
    kind: 'bool',
    summary: true,
  },
  {
    key: 'verifyNodes',
    labelKey: 'param.scan.verify_nodes',
    hintKey: 'param.scan.verify_nodes.hint',
    kind: 'bool',
  },
]

/**
 * 界面参数名 → 配置里的点号路径。
 *
 * 这一张表是「面板上的字段」与「配置项」之间唯一的换算处。参数联动校验、档位
 * 判定、另存为档位都要用它，各写一份就等于多几处会走散的地方。
 */
export const PARAM_PATHS: Record<string, string> = {
  sampleMax: 'scan.sample_max',
  workers: 'scan.workers',
  latencyThreshold: 'scan.latency_threshold',
  pingTimes: 'scan.ping_times',
  port: 'scan.port',
  timeoutMs: 'scan.timeout_ms',
  retry: 'scan.retry',
  twoPhase: 'scan.two_phase',
  verifyNodes: 'scan.verify_nodes',
}

/** 点号路径 → 界面参数名。 */
const PATH_TO_PARAM: Record<string, string> = Object.fromEntries(
  Object.entries(PARAM_PATHS).map(([name, path]) => [path, name]),
)

/** 点号路径对应的界面参数名；不属于本面板的字段返回 undefined。 */
export function paramNameOf(path: string): string | undefined {
  return PATH_TO_PARAM[path]
}

/** 自定义档位的 id。用户手改任一参数后落到这里。 */
export const CUSTOM_PRESET = 'custom'

/** 一个档位在界面上的形状：值是界面参数名。 */
export type PresetValues = Record<string, number | boolean>

/**
 * 把档位的值换算成界面参数。
 *
 * 只认面板上有的字段：档位里可能带着测速相关的键，它们不属于这个面板，硬塞
 * 进来会让面板显示一堆自己改不了的项。
 */
export function presetValues(preset: { values: Record<string, unknown> } | undefined): PresetValues {
  const out: PresetValues = {}
  for (const [path, value] of Object.entries(preset?.values ?? {})) {
    const name = PATH_TO_PARAM[path]
    if (name === undefined) continue
    if (typeof value === 'number' || typeof value === 'boolean') out[name] = value
  }
  return out
}

/**
 * 判断当前参数属于哪个档位。
 *
 * 只比档位声明过、且面板上有的那些键：用户没碰过的参数不该让档位判定失败，
 * 档位里带的测速参数也不该影响这个面板的判定。一个档位在面板上一个键都没声明
 * 时跳过它——拿不到依据就不该下结论。
 */
export function matchPreset(values: Record<string, unknown>, presets: PresetLike[]): string {
  for (const preset of presets) {
    const comparable = Object.keys(preset.values).filter((path) => PATH_TO_PARAM[path] !== undefined)
    if (comparable.length === 0) continue
    if (comparable.every((path) => values[PATH_TO_PARAM[path]!] === preset.values[path])) return preset.id
  }
  return CUSTOM_PRESET
}

/** matchPreset 只需要 id 与值，不关心档位的其它字段。 */
export interface PresetLike {
  id: string
  values: Record<string, unknown>
}

/**
 * 把界面参数换算成配置里的点号路径，用于「另存为我的档位」。
 *
 * 与 presetValues 互逆。往返一趟必须能回到原值，否则存下来的档位切回去就不
 * 是原来那组参数了。
 */
export function paramPaths(values: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [name, path] of Object.entries(PARAM_PATHS)) {
    const value = values[name]
    if (typeof value === 'number' || typeof value === 'boolean') out[path] = value
  }
  return out
}
