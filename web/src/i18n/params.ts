/**
 * 扫描参数与内置档位。
 *
 * 参数的中文标签、取值范围、说明都集中在这里，组件只负责渲染——文案散在
 * 模板里就没法统一改，也没法做双语。
 *
 * 档位是**参数快照**：选档位等于批量填充一组值，填完立刻可以改。因此这里
 * 只描述「每个档位填什么」，不描述「档位是一个关卡」。
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

export interface Preset {
  id: string
  labelKey: string
  /** 该档位填充的参数值。键名与 ScanParams 一致。 */
  values: Record<string, number | boolean>
}

/**
 * 内置档位。
 *
 * 取值来自功能规格：并发上限只到 200、采样上限只到 5000——更大的值既没必要
 * 也拖时间，界面上不提供，也不诱导用户往极端值调。
 */
export const SCAN_PRESETS: Preset[] = [
  {
    id: 'fast',
    labelKey: 'preset.fast',
    values: { sampleMax: 500, workers: 100, latencyThreshold: 300, pingTimes: 1, port: 443 },
  },
  {
    id: 'standard',
    labelKey: 'preset.standard',
    values: { sampleMax: 2000, workers: 150, latencyThreshold: 230, pingTimes: 0, port: 443 },
  },
  {
    id: 'precise',
    labelKey: 'preset.precise',
    values: { sampleMax: 5000, workers: 200, latencyThreshold: 200, pingTimes: 3, port: 443 },
  },
]

/** 自定义档位的 id。用户手改任一参数后落到这里。 */
export const CUSTOM_PRESET = 'custom'

/**
 * 判断一组参数值属于哪个档位。
 *
 * 只比档位声明过的那些键：用户没碰过的参数不该让档位判定失败。
 */
export function matchPreset(values: Record<string, unknown>): string {
  for (const preset of SCAN_PRESETS) {
    const same = Object.entries(preset.values).every(([key, want]) => values[key] === want)
    if (same) return preset.id
  }
  return CUSTOM_PRESET
}

/** 把档位的参数值展开成一份可直接用的参数对象。 */
export function presetValues(id: string): Record<string, number | boolean> {
  const preset = SCAN_PRESETS.find((item) => item.id === id)
  return preset ? { ...preset.values } : {}
}
