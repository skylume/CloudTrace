/**
 * 质量两轴：探测是否可达，测速是否达标。
 *
 * 分成两轴而不是一个四态枚举，因为「扫到了」和「跑得动」是两件事：扫描视图
 * 只能回答前者，测速视图只能回答后者。合成一个状态会让「未测速」既像失败又
 * 像正常，用户没法判断下一步该做什么。
 *
 * 两轴的判定必须与后端同口径：可达看 recv 与延迟哨兵，合格看 speed.min_speed，
 * 前端不另立一套阈值。
 */
import { UNREACHABLE, type IPRecord } from '@/api/types'

export type Reachability = 'reachable' | 'unreachable'

/** 测速结论只有三态：没测过、没到线、到了。探测失败不属于这一轴。 */
export type SpeedVerdict = 'qualified' | 'below' | 'untested'

export type QualityTone = 'ok' | 'warn' | 'muted' | 'bad'

export type QualityLabel =
  | 'quality.reachable'
  | 'quality.unreachable'
  | 'quality.qualified'
  | 'quality.below'
  | 'quality.untested'

export interface QualityMark {
  tone: QualityTone
  label: QualityLabel
}

/**
 * 探测是否成功过。
 *
 * 与后端 reachable 同一条规则：收到过回包，且延迟不是不可达哨兵。0ms 是合法
 * 的极快值，不能当成「没测到」。
 */
export function reachable(record: IPRecord): boolean {
  return record.recv > 0 && record.latency !== UNREACHABLE
}

export function reachabilityOf(record: IPRecord): Reachability {
  return reachable(record) ? 'reachable' : 'unreachable'
}

/**
 * 测速结论。
 *
 * floor <= 0 表示合格线还没读到配置，此时「测出速度」即视为合格——与结果页
 * 「仅显示达标」在 floor 为 0 时的行为一致。
 */
export function speedVerdictOf(record: IPRecord, floor: number): SpeedVerdict {
  if (!(record.speed_mbps > 0)) return 'untested'
  if (floor > 0 && record.speed_mbps < floor) return 'below'
  return 'qualified'
}

/** 当前视图该显示哪一轴，以及对应的色调与文案键。 */
export function qualityOf(record: IPRecord, speedView: boolean, floor: number): QualityMark {
  if (speedView) {
    const verdict = speedVerdictOf(record, floor)
    if (verdict === 'qualified') return { tone: 'ok', label: 'quality.qualified' }
    if (verdict === 'below') return { tone: 'warn', label: 'quality.below' }
    return { tone: 'muted', label: 'quality.untested' }
  }
  if (reachable(record)) return { tone: 'muted', label: 'quality.reachable' }
  return { tone: 'bad', label: 'quality.unreachable' }
}

/**
 * 质量排序用的等级：合格 > 低于阈值 > 未测速 > 不可达。
 *
 * 与视图无关：排序是一份固定的「谁更值得先看」，切视图只换标签，不换顺序，
 * 否则用户切一下视图，刚记住的名次就全变了。
 */
export function qualityRank(record: IPRecord, floor: number): number {
  if (!reachable(record)) return 0
  const verdict = speedVerdictOf(record, floor)
  if (verdict === 'qualified') return 3
  if (verdict === 'below') return 2
  return 1
}
