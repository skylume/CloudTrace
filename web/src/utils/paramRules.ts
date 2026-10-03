/**
 * 参数联动校验。
 *
 * 单个参数都在合理范围内、合起来却不合理的组合，靠逐项校验是发现不了的。
 * 这里只**提示**，绝不自动改——规格把「参数联动」明确划在自适应之外：自动
 * 逻辑只在「不改会让你拿到更差结果」时出手，而组合问题通常取决于用户想
 * 干什么（并发拉高又缩短间隔，也可能是他清楚自己在做什么）。
 *
 * 规则用配置里的点号路径书写（`speed.concurrency`），与 `settings/update`
 * 接受的键一致。两个入口共用同一张表：
 *   - `checkCombinations` 收嵌套配置，给设置页用；
 *   - `checkScanParams` 收扫描页那份扁平驼峰参数，内部转成点号路径。
 *
 * 返回的是机器可读的规则标识，文案由界面层决定（与自适应、选源说明同一口径）。
 */

import { paramNameOf } from '@/i18n/params'

export interface ParamWarning {
  /** 规则标识，界面据此选文案。 */
  rule: string
  /**
   * 涉及的参数路径，顺序与 `values` 一致。
   *
   * 只列 `hit` 真正读过的那几个键：`hit` 返回 true 就说明它们都取到了值，
   * `values` 才能与 `keys` 一一对齐。
   */
  keys: string[]
  /**
   * 命中时各参数的实际取值，顺序与 `keys` 一致。
   *
   * 一并带出来是为了让界面层不必再读一遍配置：设置页读的是嵌套配置、
   * 扫描页读的是扁平驼峰参数，两处的读法不同，让界面各读一次就等于把
   * 「哪个键对应哪个值」这件事写了两遍。
   */
  values: number[]
}

/** 读取一个数值参数；取不到或不是数字就当它不存在，不参与判断。 */
function readPath(values: Record<string, unknown>, path: string): number | null {
  let current: unknown = values
  for (const part of path.split('.')) {
    if (current === null || typeof current !== 'object') return null
    current = (current as Record<string, unknown>)[part]
  }
  return typeof current === 'number' && Number.isFinite(current) ? current : null
}

interface Rule {
  id: string
  keys: string[]
  /** 命中条件。任一所依赖的参数取不到时不会命中。 */
  hit: (read: (path: string) => number | null) => boolean
}

const RULES: Rule[] = [
  {
    id: 'speed_concurrency_vs_interval',
    keys: ['speed.concurrency', 'speed.interval_ms'],
    // 并发高又几乎不留间隔，等于对着测速源猛冲，很快会被限速——
    // 而限速一旦连续触发就会熔断，整轮测速白跑。
    hit: (read) => {
      const concurrency = read('speed.concurrency')
      const interval = read('speed.interval_ms')
      return concurrency !== null && interval !== null && concurrency > 5 && interval < 1000
    },
  },
  {
    id: 'scan_workers_vs_timeout',
    keys: ['scan.workers', 'scan.timeout_ms'],
    // 并发很高而超时很短时，大量请求会在排队中就被判成超时，结果里会出现
    // 一批「不可达」，而它们其实只是没排上队。
    hit: (read) => {
      const workers = read('scan.workers')
      const timeout = read('scan.timeout_ms')
      return workers !== null && timeout !== null && workers > 100 && timeout < 500
    },
  },
  {
    id: 'threshold_vs_sample',
    keys: ['scan.latency_threshold', 'scan.sample_max'],
    // 阈值卡得很紧又只采很少的地址，很可能一个都过不了，白等一轮。
    // 采样为 0 表示不限制，不算「采得少」。
    hit: (read) => {
      const threshold = read('scan.latency_threshold')
      const sample = read('scan.sample_max')
      return threshold !== null && sample !== null && threshold < 100 && sample > 0 && sample < 500
    },
  },
]

function collect(read: (path: string) => number | null): ParamWarning[] {
  const out: ParamWarning[] = []
  for (const rule of RULES) {
    if (!rule.hit(read)) continue
    const values = rule.keys.map((key) => read(key)).filter((value): value is number => value !== null)
    out.push({ rule: rule.id, keys: rule.keys, values })
  }
  return out
}

/** 检查嵌套配置里的参数组合（设置页）。 */
export function checkCombinations(config: Record<string, unknown>): ParamWarning[] {
  return collect((path) => readPath(config, path))
}

/**
 * 检查扫描页参数面板里的组合（参数是扁平驼峰命名）。
 *
 * 路径换算复用面板那份唯一的映射表：各写一份就等于多一处会走散的地方。
 */
export function checkScanParams(params: Record<string, unknown>): ParamWarning[] {
  return collect((path) => {
    const name = paramNameOf(path)
    return name === undefined ? null : readPath(params, name)
  })
}
