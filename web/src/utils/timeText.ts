/**
 * 时间显示。
 *
 * 列表里显示相对时间（「12 分钟前」比「14:05」更能说明新旧），悬停给出准确时间。
 * 准确时间按配置里的时区偏好显示——跨时区对照日志时 UTC 省事，日常看本地时间
 * 更直观。
 *
 * 这里只做换算，不碰文案：返回单位与数值，由界面层套进 i18n。工具函数里直接
 * 取翻译会让它在两种语言下各自需要一份用例，而且绕不开「工具层依赖界面层」。
 */

/** 相对时间的单位。界面据此选文案。 */
export type AgeUnit = 'justNow' | 'minutes' | 'hours' | 'days'

export interface AgeText {
  unit: AgeUnit
  /** 数值；justNow 时为 0，界面忽略它。 */
  value: number
}

/**
 * 把时间戳换算成相对时间。
 *
 * now 由调用方传入：取当前时间这件事本身不该藏在工具函数里，否则它没法测。
 */
export function ageOf(iso: string, now: number): AgeText {
  const at = new Date(iso).getTime()
  // 认不出来的时间不给一个假的相对值，交给调用方原样显示。
  if (!Number.isFinite(at)) return { unit: 'justNow', value: 0 }

  const minutes = Math.round((now - at) / 60000)
  // 未来时间（时钟偏差、后端时间略快）按「刚刚」处理，不显示负数分钟。
  if (minutes < 1) return { unit: 'justNow', value: 0 }
  if (minutes < 60) return { unit: 'minutes', value: minutes }

  const hours = Math.round(minutes / 60)
  if (hours < 24) return { unit: 'hours', value: hours }
  return { unit: 'days', value: Math.round(hours / 24) }
}

/** 相对时间能不能算出来。算不出来时调用方原样显示后端给的字符串。 */
export function hasAge(iso: string): boolean {
  return Number.isFinite(new Date(iso).getTime())
}

/**
 * 绝对时间：`YYYY-MM-DD HH:mm`，UTC 模式带后缀。
 *
 * 不用 toLocaleString：它的输出随运行环境的区域设置变（同样的值在中文与英文
 * 系统上不一样），而这个字符串是要拿去和日志对照的，格式必须稳定。
 */
export function formatAbsolute(iso: string, mode: 'local' | 'utc'): string {
  const at = new Date(iso)
  if (!Number.isFinite(at.getTime())) return iso

  const pad = (value: number) => String(value).padStart(2, '0')
  const date =
    (mode === 'utc' ? at.getUTCFullYear() : at.getFullYear()) +
    '-' +
    pad((mode === 'utc' ? at.getUTCMonth() : at.getMonth()) + 1) +
    '-' +
    pad(mode === 'utc' ? at.getUTCDate() : at.getDate())
  const time =
    pad(mode === 'utc' ? at.getUTCHours() : at.getHours()) +
    ':' +
    pad(mode === 'utc' ? at.getUTCMinutes() : at.getMinutes())

  return date + ' ' + time + (mode === 'utc' ? ' UTC' : '')
}
