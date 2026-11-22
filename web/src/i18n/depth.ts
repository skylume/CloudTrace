/**
 * 扫描深度：三档纯预设。
 *
 * 它不是「档位」——不命名、不保存、不导入导出。点一下把一组值填进面板，
 * 之后用户怎么改都与它无关，界面也不显示「已偏离」：档位一旦有状态，
 * 就会长出「另存为」「置顶」「设为启动档位」那一整套东西。
 *
 * 保留它的理由只有一个：十五个扫描参数总得有个锚点，「采 2000 个、并发 150」
 * 到底要跑多久，新手说不清。
 */
export type ScanDepth = 'fast' | 'standard' | 'precise'

export interface DepthSpec {
  id: ScanDepth
  labelKey: string
  noteKey: string
  /** 值是面板参数名（驼峰），与 PARAM_WIRE_KEYS 的键同形。 */
  values: Record<string, number | boolean>
}

export const SCAN_DEPTHS: DepthSpec[] = [
  {
    id: 'fast',
    labelKey: 'depth.fast',
    noteKey: 'depth.fast.note',
    // 一分钟内出结果：少采样、单次探测、不解析节点明细。
    values: {
      sampleMax: 500,
      workers: 100,
      latencyThreshold: 300,
      pingTimes: 1,
      twoPhase: true,
      verifyNodes: false,
    },
  },
  {
    id: 'standard',
    labelKey: 'depth.standard',
    noteKey: 'depth.standard.note',
    values: {
      sampleMax: 2000,
      workers: 150,
      latencyThreshold: 230,
      // 0 表示自动（IPv4 三次 / IPv6 两次），与后端语义一致。
      pingTimes: 0,
      twoPhase: true,
      verifyNodes: true,
    },
  },
  {
    id: 'precise',
    labelKey: 'depth.precise',
    noteKey: 'depth.precise.note',
    values: {
      sampleMax: 5000,
      workers: 200,
      latencyThreshold: 200,
      pingTimes: 3,
      twoPhase: true,
      verifyNodes: true,
    },
  },
]

export const DEFAULT_DEPTH: ScanDepth = 'standard'

/** 某一档要填的那组值；返回副本，调用方改了不会污染常量表。 */
export function depthValues(id: ScanDepth): Record<string, number | boolean> {
  const found = SCAN_DEPTHS.find((item) => item.id === id)
  return { ...(found?.values ?? {}) }
}
