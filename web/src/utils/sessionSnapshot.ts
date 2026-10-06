/**
 * 未保存结果的会话快照。
 *
 * 扫描结果只活在内存里，而扫描要跑一分钟左右——这期间刷新页面、浏览器崩掉、
 * 手滑关了标签页，那一分钟就白跑了。历史体系救不了这种场景：它只保存**已完成**
 * 的任务，中途的结果按设计不写历史（一份不完整的记录混在列表里，用户分不清
 * 它和一次正常扫描的区别）。
 *
 * 所以这里存一份临时快照，让「刚才那一分钟」能捞回来。几个刻意的取舍：
 *
 * - 存 localStorage 而不是数据目录：结果本来就在前端手里，为它跑一趟服务端
 *   只是绕路；而需要恢复的恰恰是**前端没了**这种情况，localStorage 正好活过
 *   一次刷新与一次标签页崩溃。
 * - 只在任务进行中写、任务一结束就删：它不是归档，是一个「还热着的」缓存。
 * - 有上限：localStorage 只有几 MB，写爆了会连带把界面偏好一起挤掉。
 */
import type { IPRecord } from '@/api/types'

/** 本地存储键。 */
const KEY = 'cloudtrace.session'

/**
 * 最多存多少条。
 *
 * 一条记录约 200 字节，2000 条约 400KB——留足余量，不至于把 localStorage
 * 写爆。超出的部分按「先到先得」保留：用户真正在意的是已经扫出来的那些，
 * 而不是最后涌进来的那批。
 */
const MAX_RECORDS = 2000

export interface SessionSnapshot {
  /** 写入时间（毫秒时间戳）。 */
  savedAt: number
  /** 写入时的阶段：scan / speed。 */
  phase: string
  /** 当时的总数，用于提示「有 N 条」。 */
  total: number
  records: IPRecord[]
}

/** 写入快照。写不进去（配额满、隐私模式）就静默放弃——它只是个便利功能。 */
export function saveSnapshot(snapshot: SessionSnapshot): void {
  try {
    const trimmed: SessionSnapshot = {
      ...snapshot,
      records: snapshot.records.slice(0, MAX_RECORDS),
    }
    localStorage.setItem(KEY, JSON.stringify(trimmed))
  } catch {
    /* 配额满或被禁用，忽略 */
  }
}

/** 读回快照。读不出来返回 null。 */
export function loadSnapshot(): SessionSnapshot | null {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<SessionSnapshot>
    if (!Array.isArray(parsed.records) || parsed.records.length === 0) return null
    if (typeof parsed.savedAt !== 'number') return null
    return {
      savedAt: parsed.savedAt,
      phase: typeof parsed.phase === 'string' ? parsed.phase : '',
      total: typeof parsed.total === 'number' ? parsed.total : parsed.records.length,
      records: parsed.records as IPRecord[],
    }
  } catch {
    return null
  }
}

export function clearSnapshot(): void {
  try {
    localStorage.removeItem(KEY)
  } catch {
    /* 忽略 */
  }
}

/**
 * 快照值不值得提示恢复。
 *
 * 只扫出个位数节点的情况不提示：那种结果重扫一次比点一次「恢复」还快，而多
 * 一次打断就多一次让用户觉得「这程序怎么老弹东西」的机会。
 */
export const MIN_RESTORE_RECORDS = 10

export function worthRestoring(snapshot: SessionSnapshot): boolean {
  return snapshot.records.length >= MIN_RESTORE_RECORDS
}
