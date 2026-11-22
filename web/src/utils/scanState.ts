/**
 * 扫描页参数的取值来源。
 *
 * 面板上的参数有两个可能的来源，优先级不能含糊：
 *   1. 用户显式设置过（配置里有 `scan.*` 的来源标记）→ 用配置里的值；
 *   2. 从没设置过（首次运行）→ 用**启动档位**填一遍；
 *   3. 配置里没有这一项 → 保持面板初值，不凭空造一个数。
 *
 * 面板上改的值只影响本次任务，不写回配置——那是设置页的事。曾经这里会把改动
 * 差分后写回，用户以为在「调一下试试」，实际改了全局默认。
 *
 * 为什么首次运行不直接用配置里的默认值：配置的默认值是给无人值守的调用方
 * 用的保守取值（采满、并发拉高），而界面上的第一印象应该是「点一下就能出结
 * 果」。这两者本来就不是一回事，硬把它们合成一份，改任何一边都会伤到另一边。
 */
import { PARAM_WIRE_KEYS } from '@/i18n/params'
import type { ParamOrigins } from '@/api/types'

/**
 * 判断扫描参数是否「从没被显式设置过」。
 *
 * 认整组标记（`scan`）与单参数标记（`scan.workers`）两种粒度：恢复默认是按
 * 单参数做的，只认单参数标记的话，整组恢复过的配置会被误判成「设置过」。
 */
export function isUntouchedScan(origins: ParamOrigins | undefined): boolean {
  for (const key of Object.keys(origins ?? {})) {
    if (key === 'scan' || key.startsWith('scan.')) return false
  }
  return true
}

/** 从配置的 scan 分组里取出面板认识的参数；认不出的键忽略。 */
export function scanParamsFromConfig(
  scan: Record<string, unknown> | undefined,
): Record<string, number | boolean> {
  const out: Record<string, number | boolean> = {}
  for (const [name, wire] of Object.entries(PARAM_WIRE_KEYS)) {
    const value = scan?.[wire]
    if (typeof value === 'number' || typeof value === 'boolean') out[name] = value
  }
  return out
}


