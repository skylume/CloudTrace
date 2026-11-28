/**
 * 延迟分档：把「42.3 ms」变成一眼可读的信号。
 *
 * 分档规则集中在这里，不散落到各个组件：表格、卡片、详情、移动端必须用
 * 同一套判定，否则同一行数据在两处显示成不同颜色，用户会先怀疑数据错了。
 */
import { UNREACHABLE } from '@/api/types'

export type LatencyTier = 'fastest' | 'fast' | 'mid' | 'slow' | 'dead'

/** 探测模式。与配置里的 scan.mode 同值。 */
export type LatencyMode = 'tcping' | 'httping'

/** 分档边界，单位毫秒。由快到慢，取「小于该值即落入此档」。 */
const BOUNDS: { tier: LatencyTier; max: number }[] = [
  { tier: 'fastest', max: 100 },
  { tier: 'fast', max: 180 },
  { tier: 'mid', max: 250 },
]

/** 档位对应的信号格数。格数是最可靠的编码，色盲与灰度打印下也读得出。 */
const BARS: Record<LatencyTier, number> = {
  fastest: 4,
  fast: 3,
  mid: 2,
  slow: 1,
  dead: 0,
}

/** 档位对应的颜色变量。极快与快共用一档颜色，靠格数区分。 */
const COLORS: Record<LatencyTier, string> = {
  fastest: 'var(--latency-fast)',
  fast: 'var(--latency-fast)',
  mid: 'var(--latency-mid)',
  slow: 'var(--latency-slow)',
  dead: 'var(--latency-dead)',
}

/**
 * 判定一个延迟值属于哪一档。
 *
 * 哨兵值与非法值一律按「不可达」处理：0 是合法的极快延迟，不能被当成
 * 「没有数据」，两者必须区分开。
 */
export function latencyTier(latency: number): LatencyTier {
  if (!Number.isFinite(latency) || latency < 0 || latency === UNREACHABLE) return 'dead'
  for (const bound of BOUNDS) {
    if (latency < bound.max) return bound.tier
  }
  return 'slow'
}

/** 返回该档位的实心格数（0..4）。 */
export function tierBars(tier: LatencyTier): number {
  return BARS[tier]
}

/** 返回该档位对应的颜色变量名。 */
export function tierColorVar(tier: LatencyTier): string {
  return COLORS[tier]
}

/**
 * 格式化延迟。
 *
 * 不可达显示成破折号而不是 0：0 是合法的极快值，用它表示「没测到」会误导。
 */
export function formatLatency(latency: number, digits = 1): string {
  if (latencyTier(latency) === 'dead') return '—'
  return latency.toFixed(digits)
}

/** 格式化下载速度；未测速时给破折号。 */
export function formatSpeed(speed: number, digits = 1): string {
  if (!Number.isFinite(speed) || speed <= 0) return '—'
  return speed.toFixed(digits)
}

/** 把丢包率格式化成百分比；没有样本时给破折号。 */
export function formatLoss(loss: number, sent: number): string {
  if (sent <= 0) return '—'
  return `${Math.round(loss * 100)}%`
}

/**
 * 与后端 probe 同源的阈值倍率。
 *
 * HTTPing 多了一次完整请求，RTT 天然比 TCP 握手长；后端判「延迟达标」时按
 * 这个倍率放宽，前端分档必须用同一个数，否则同一节点会一处合格、一处标红。
 * 只有 httping 才乘，tcping 无论是否 TLS 都是 1。
 *
 * 调用方有两种用法，不要合并成一个全局开关：
 *   - SignalBar / normalizedLatency 逐行传该记录的 use_tls，得到每行自己的
 *     等效值；
 *   - 结果页的 maxLatency 是单一标量，只能取批内最大倍率做保守近似。
 */
export function thresholdMultiplier(mode: LatencyMode, useTLS: boolean): number {
  if (mode !== 'httping') return 1
  return useTLS ? 4 : 1.3
}

/**
 * 把延迟换算到「与 tcping 可比的尺度」，用于分档。
 *
 * 显示值仍是原始延迟：用户测到多少就该看到多少，归一化只影响颜色与格数。
 */
export function normalizedLatency(latency: number, mode: LatencyMode, useTLS: boolean): number {
  if (latencyTier(latency) === 'dead') return latency
  const multiplier = thresholdMultiplier(mode, useTLS)
  return multiplier === 1 ? latency : latency / multiplier
}

/** 按当前探测模式分档。latencyTier 保持原签名，只给需要归一化的调用点用这个。 */
export function latencyTierFor(latency: number, mode: LatencyMode, useTLS: boolean): LatencyTier {
  return latencyTier(normalizedLatency(latency, mode, useTLS))
}
