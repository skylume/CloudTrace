/**
 * 组装测速任务的完整参数。
 *
 * `speed/start` 的载荷是**一份完整的参数对象**，后端不替调用方去读配置——
 * 它只按自己的默认值补缺项。所以只发 `{scope, targets}` 的话，设置页里那些
 * 测速项（并发、间隔、测速源、权重……）就全都停在了界面上，任务跑的其实是
 * 后端的默认值。这里把它们补全，与扫描页组装扫描参数是同一件事。
 *
 * 缺省一律按配置取值，配置里也没有才落到后端默认值：读配置而不是读一份
 * 前端常量，改设置页立刻生效。
 */
import type { IPRecord, Settings, SpeedParams } from '@/api/types'

/** 从配置分组里取一个数；取不到就用后端默认值。 */
function num(group: Record<string, unknown> | undefined, key: string, fallback: number): number {
  const value = group?.[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

function text(group: Record<string, unknown> | undefined, key: string, fallback: string): string {
  const value = group?.[key]
  return typeof value === 'string' && value !== '' ? value : fallback
}

function bool(group: Record<string, unknown> | undefined, key: string, fallback: boolean): boolean {
  const value = group?.[key]
  return typeof value === 'boolean' ? value : fallback
}

/**
 * 判断这批目标属于哪个地址族。
 *
 * 从目标本身推而不是读 `net.ip_version`：后者说的是「扫描时测哪个族」，
 * 而这里要归档的是「这次测速实际测的是哪个族」——结果里全是 v6 地址却归档
 * 到 v4 的话，历史列表里那一份就再也找不回来了。
 */
export function inferIPVersion(targets: IPRecord[]): number {
  if (targets.length === 0) return 4
  return targets.every((item) => item.ip.includes(':')) ? 6 : 4
}

export interface SpeedStartOptions {
  scope: string
  targets: IPRecord[]
}

/** 把配置与本次目标组装成 `speed/start` 的载荷。 */
export function buildSpeedParams(
  settings: Settings | null | undefined,
  options: SpeedStartOptions,
): SpeedParams {
  const speed = settings?.speed
  const net = settings?.net
  const scan = settings?.scan

  return {
    scope: options.scope,
    targets: options.targets,
    ip_version: inferIPVersion(options.targets),

    url_mode: text(speed, 'url_mode', 'auto'),
    custom_url: text(speed, 'custom_url', ''),
    use_tls: text(net, 'use_tls', 'auto'),

    concurrency: num(speed, 'concurrency', 1),
    target_qualified: num(speed, 'target_qualified', 10),
    interval_ms: num(speed, 'interval_ms', 1200),
    min_speed: num(speed, 'min_speed', 0),

    weight_speed: num(speed, 'weight_speed', 1),
    weight_latency: num(speed, 'weight_latency', 1),
    weight_jitter: num(speed, 'weight_jitter', 0),

    per_region_topn: num(speed, 'per_region_topn', 0),
    download_duration_s: num(speed, 'download_duration_s', 10),
    breaker_429: num(speed, 'breaker_429', 3),

    // 可用性校验的开关在扫描分组里（`scan.usability_check`），这是它在配置
    // 里唯一的家：它管的是「测速前再确认一次节点还活着」。
    usability_check: bool(scan, 'usability_check', true),
    // 0 表示交给后端按默认超时决定。这个值没有对应的配置项，不在这里编一个。
    timeout_ms: 0,
  }
}
